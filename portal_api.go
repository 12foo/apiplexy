package apiplexy

import (
	"encoding/json"
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/dgrijalva/jwt-go"
	"github.com/garyburd/redigo/redis"
	"gopkg.in/labstack/echo.v1"
	"net/http"
	"strings"
	"time"
)

type portalAPI struct {
	signingKey []byte
	linkBase   string
	m          ManagementBackendPlugin
	a          *apiplex
	keytypes   map[string]KeyType
	keyplugins map[string]AuthPlugin
}

type keyWithQuota struct {
	Key   *Key         `json:"key"`
	Quota apiplexQuota `json:"quota"`
	Avg   float64      `json:"avg"`
}

func abort(res http.ResponseWriter, code int, message string, args ...interface{}) {
	res.Header().Set("Content-Type", "application/json;charset=utf-8")
	res.WriteHeader(code)
	e := struct {
		Error string `json:"error"`
	}{Error: fmt.Sprintf(message, args...)}
	j, _ := json.Marshal(&e)
	res.Write(j)
}

func finish(res http.ResponseWriter, result interface{}) {
	res.Header().Set("Content-Type", "application/json;charset=utf-8")
	res.WriteHeader(http.StatusOK)
	json.NewEncoder(res).Encode(result)
}

func (p *portalAPI) createUser(res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	n := struct {
		Email    string
		Name     string
		Password string
		Profile  map[string]interface{}
		Link     string
	}{}
	if decoder.Decode(&n) != nil || n.Email == "" || n.Password == "" || n.Name == "" || n.Link == "" {
		abort(res, 400, "Request a new account by supplying email, name, password and a template for an activation link.")
		return
	}
	u := User{Name: n.Name, Email: n.Email, Active: false, Profile: n.Profile}
	err := p.m.AddUser(n.Email, n.Password, &u)
	if err != nil {
		abort(res, 400, "Could not create new account: %s", err.Error())
		return
	}
	if !u.Active {
		code := uniuri.NewLen(48)
		link := strings.Replace(n.Link, "CODE", code, 1)
		r := p.a.redis.Get()
		r.Do("SETEX", "activation:"+code, (24 * time.Hour).Seconds(), n.Email)

		p.a.sendEmail(n.Email, "Activate your account", "text/plain", fmt.Sprintf(`Hi %s,

please activate your developer account by visiting this link:
%s
`, u.Name, link))
	}
	finish(res, &u)
}

func (p *portalAPI) activateUser(c *echo.Context) error {
	res := c.Response().Writer()
	activationKey := c.Param("key")
	r := p.a.redis.Get()
	email, err := redis.String(r.Do("GET", "activation:"+activationKey))
	if err != nil {
		if err == redis.ErrNil {
			abort(res, 403, "Invalid or expired activation code.")
			return nil
		} else {
			abort(res, 500, err.Error())
		}
	}
	if err = p.m.ActivateUser(email); err != nil {
		abort(res, 500, "Could not activate account: %s", err.Error())
		return nil
	}
	r.Do("DEL", "activation:"+activationKey)
	finish(res, map[string]interface{}{
		"success": "Activation successful. Please return to the login page.",
	})
	return nil
}

func (p *portalAPI) getToken(res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	login := struct {
		Email    string
		Password string
	}{}
	if decoder.Decode(&login) != nil || login.Email == "" || login.Password == "" {
		abort(res, 400, "Log in by supplying your email and password.")
		return
	}
	u := p.m.Authenticate(login.Email, login.Password)
	if u == nil {
		abort(res, 403, "Wrong email/password combination.")
		return
	}
	token := jwt.New(jwt.SigningMethodHS256)
	token.Claims["email"] = u.Email
	token.Claims["exp"] = time.Now().Add(time.Hour * 12).Unix()
	ts, err := token.SignedString(p.signingKey)
	if err != nil {
		abort(res, 500, "Could not create authentication token: %s", err.Error())
		return
	}
	tjson := struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Token string `json:"token"`
	}{u.Name, u.Email, ts}
	finish(res, &tjson)
}

func (p *portalAPI) updateProfile(email string, res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	r := struct {
		Name    string
		Profile map[string]interface{}
	}{}
	if decoder.Decode(&r) != nil {
		abort(res, 400, "Supply a new name, a new profile, or both.")
		return
	}
	u := p.m.GetUser(email)
	if u == nil {
		abort(res, 404, "Your user was not found. Please log in again.")
		return
	}
	if r.Name != "" {
		u.Name = r.Name
	}
	if len(r.Profile) > 0 {
		u.Profile = r.Profile
	}
	if err := p.m.UpdateUser(email, u); err != nil {
		abort(res, 500, "Couldn't update user profile: %s", err.Error())
		return
	}
	finish(res, u)
}

func (p *portalAPI) getKeyTypes(email string, res http.ResponseWriter, req *http.Request) {
	finish(res, p.keytypes)
}

func (p *portalAPI) getAllKeys(email string, res http.ResponseWriter, req *http.Request) {
	keys, err := p.m.GetAllKeys(email)
	if err != nil {
		abort(res, 500, err.Error())
		return
	}

	results := make([]keyWithQuota, len(keys))
	if len(results) == 0 {
		finish(res, results)
		return
	}

	avgs := make([]float64, len(keys))
	redisAvgKeys := make([]interface{}, len(keys))
	for i, k := range keys {
		redisAvgKeys[i] = "quota:key:" + k.ID + ":avg"
	}
	r := p.a.redis.Get()
	rawAvgs, err := redis.Values(r.Do("MGET", redisAvgKeys...))
	if err != nil {
		abort(res, 500, err.Error())
	}
	if err = redis.ScanSlice(rawAvgs, &avgs); err != nil {
		abort(res, 500, err.Error())
	}

	for i, k := range keys {
		q, ok := p.a.quotas[k.Quota]
		if !ok {
			q = p.a.quotas["default"]
		}
		kwq := keyWithQuota{Key: k, Quota: q, Avg: avgs[i]}
		results[i] = kwq
	}

	finish(res, results)
}

func (p *portalAPI) createKey(email string, res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	r := struct {
		Type  string `json:"type"`
		Realm string `json:"realm"`
	}{}
	if decoder.Decode(&r) != nil || r.Type == "" {
		abort(res, 400, "Specify a key_type.")
		return
	}
	plugin, found := p.keyplugins[r.Type]
	if !found {
		abort(res, 400, "The requested key type is not available for creation.")
		return
	}
	key, err := plugin.Generate(r.Type)
	key.Realm = r.Realm
	if err != nil {
		abort(res, 500, "Could not create %s key: %s", r.Type, err.Error())
		return
	}
	if err = p.m.AddKey(email, &key); err != nil {
		abort(res, 500, "The new key could not be stored. %s", err.Error())
		return
	}

	q, ok := p.a.quotas[key.Quota]
	if !ok {
		q = p.a.quotas["default"]
	}
	finish(res, keyWithQuota{Key: &key, Quota: q, Avg: 0})
}

func (p *portalAPI) deleteKey(email string, res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	r := struct {
		KID string `json:"key_id"`
	}{}
	if decoder.Decode(&r) != nil || r.KID == "" {
		abort(res, 400, "Specify a key_id to delete.")
		return
	}
	if err := p.m.DeleteKey(email, r.KID); err != nil {
		abort(res, 500, "Could not delete key: %s", err.Error())
		return
	}
	msg := struct {
		Deleted string `json:"deleted"`
	}{Deleted: r.KID}
	finish(res, &msg)
}

func (p *portalAPI) requestResetPassword(res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	rq := struct {
		Email string `json:"email"`
		Link  string `json:"link"`
	}{}
	if decoder.Decode(&rq) != nil || rq.Email == "" || rq.Link == "" {
		abort(res, 400, "Specify your email and a link to reset your password.")
		return
	}
	u := p.m.GetUser(rq.Email)
	if u == nil {
		abort(res, 404, "User not found.")
		return
	}
	code := uniuri.NewLen(48)
	r := p.a.redis.Get()
	_, err := r.Do("SETEX", "password-reset:"+code, 60*60, rq.Email)
	if err != nil {
		abort(res, 500, "Couldn't store your password reset request. Please contact an administrator.")
		return
	}

	p.a.sendEmail(rq.Email, "Reset your password", "text/plain; charset=UTF-8", fmt.Sprintf(`Hi %s,

to reset your password, please visit this link:
%s
`, u.Name, strings.Replace(rq.Link, "CODE", code, 1)))

	finish(res, &rq)
}

func (p *portalAPI) resetPassword(res http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	rq := struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}{}
	if decoder.Decode(&rq) != nil || rq.Code == "" || rq.Password == "" {
		abort(res, 400, "Provide the reset code and a new password to reset your password.")
		return
	}

	r := p.a.redis.Get()
	email, err := redis.String(r.Do("GET", "password-reset:"+rq.Code))
	if err != nil {
		abort(res, 400, "Invalid or expired password reset code.")
		return
	}
	err = p.m.ResetPassword(email, rq.Password)
	if err != nil {
		abort(res, 500, err.Error())
	}
	r.Do("DEL", "password-reset:"+rq.Code)
	finish(res, map[string]interface{}{"success": "Password successfully reset."})
}

func (p *portalAPI) auth(inner func(string, http.ResponseWriter, *http.Request)) func(*echo.Context) error {
	return func(c *echo.Context) error {
		res := c.Response().Writer()
		req := c.Request()
		token, err := jwt.ParseFromRequest(req, func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("Token signed with an incorrect method: %v", token.Header["alg"])
			}
			return p.signingKey, nil
		})
		if err != nil {
			abort(res, 403, "Access denied: %s -- please authenticate using a valid token.", err.Error())
			return nil
		}
		email, ok := token.Claims["email"].(string)
		if !ok {
			abort(res, 403, "Access denied: user token did not supply a valid user.", err.Error())
			return nil
		}
		inner(email, res, req)
		return nil
	}
}

func (ap *apiplex) buildPortalAPI() (*portalAPI, error) {
	if ap.usermgmt == nil {
		return nil, fmt.Errorf("Cannot create portal API. There is no backend plugin that supports full user management.")
	}

	availKeytypes := make(map[string]KeyType)
	keyPlugins := make(map[string]AuthPlugin)
	for _, authplug := range ap.auth {
		for _, kt := range authplug.AvailableTypes() {
			availKeytypes[kt.Name] = kt
			keyPlugins[kt.Name] = authplug
		}
	}

	return &portalAPI{
		signingKey: []byte(ap.signingKey),
		m:          ap.usermgmt,
		a:          ap,
		keytypes:   availKeytypes,
		keyplugins: keyPlugins,
	}, nil
}

func middleware() echo.HandlerFunc {
	return func(c *echo.Context) error {
		return nil
	}
}

func (ap *apiplex) BuildPortalAPI(mux *echo.Echo, path string) (*echo.Group, error) {
	p, err := ap.buildPortalAPI()
	if err != nil {
		return nil, err
	}

	r := mux.Group(path)
	r.Use(middleware())
	r.Post("/account", p.createUser)
	r.Get("/account/activate/:key", p.activateUser)
	r.Post("/account/request", p.requestResetPassword)
	r.Post("/account/reset", p.resetPassword)
	r.Post("/account/token", p.getToken)
	r.Post("/account/update", p.auth(p.updateProfile))
	r.Get("/keys/types", p.auth(p.getKeyTypes))
	r.Get("/keys", p.auth(p.getAllKeys))
	r.Post("/keys", p.auth(p.createKey))
	r.Post("/keys/delete", p.auth(p.deleteKey))

	// r := mux.NewRouter().PathPrefix(prefix).MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
	// 	if r.Method == "GET" {
	// 		return true
	// 	}
	// 	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
	// }).Subrouter()

	return r, nil
}
