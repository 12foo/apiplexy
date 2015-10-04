package apiplexy

import (
	"encoding/json"
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/dgrijalva/jwt-go"
	"github.com/garyburd/redigo/redis"
	"github.com/gorilla/mux"
	"net/http"
	"strings"
	"time"
)

type portalAPI struct {
	signingKey []byte
	m          ManagementBackendPlugin
	a          *apiplex
	keytypes   map[string]KeyType
	keyplugins map[string]AuthPlugin
}

type activationData struct {
	Email string `json:"email"`
	After string `json:"after"`
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
		After    string
	}{}
	if decoder.Decode(&n) != nil || n.Email == "" || n.Password == "" || n.Name == "" {
		abort(res, 400, "Request a new account by supplying your email, name and password.")
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
		act := activationData{Email: n.Email, After: n.After}
		r := p.a.redis.Get()
		jsonAct, _ := json.Marshal(&act)
		r.Do("SETEX", "activation:"+code, (24 * time.Hour).Seconds(), jsonAct)
		// TODO send activation code email
	}
	finish(res, &u)
}

func (p *portalAPI) activateUser(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	activationKey := vars["key"]
	r := p.a.redis.Get()
	actJson, err := redis.Bytes(r.Do("GET", "activation:"+activationKey))
	if err != nil {
		if err == redis.ErrNil {
			abort(res, 403, "Invalid or expired activation code.")
			return
		} else {
			abort(res, 500, err.Error())
		}
	}
	act := activationData{}
	json.Unmarshal(actJson, &act)
	if act.Email == "" {
		abort(res, 500, "Invalid activation data.")
		return
	}
	if p.m.ActivateUser(act.Email) != nil {
		abort(res, 500, "Could not activate account: %s", err.Error())
		return
	}
	r.Do("DELETE", "activation:"+activationKey)
	if act.After != "" {
		http.Redirect(res, req, act.After, 302)
	} else {
		res.WriteHeader(http.StatusOK)
		res.Write([]byte("Activation successful. Please return to the login page and log in."))
	}
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
		Token string `json:"token"`
	}{Token: ts}
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
	finish(res, keys)
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
	finish(res, &key)
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

func (p *portalAPI) auth(inner func(string, http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(res http.ResponseWriter, req *http.Request) {
		token, err := jwt.ParseFromRequest(req, func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("Token signed with an incorrect method: %v", token.Header["alg"])
			}
			return p.signingKey, nil
		})
		if err != nil {
			abort(res, 403, "Access denied: %s -- please authenticate using a valid token.", err.Error())
			return
		}
		email, ok := token.Claims["email"].(string)
		if !ok {
			abort(res, 403, "Access denied: user token did not supply a valid user.", err.Error())
			return
		}
		inner(email, res, req)
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

func (ap *apiplex) BuildPortalAPI(prefix string) (*mux.Router, error) {
	p, err := ap.buildPortalAPI()
	if err != nil {
		return nil, err
	}

	r := mux.NewRouter().PathPrefix(prefix).MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
		if r.Method == "GET" {
			return true
		}
		return strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
	}).Subrouter()

	r.HandleFunc("/account", p.createUser).Methods("POST")
	r.HandleFunc("/account/activate/{key}", p.activateUser)
	r.HandleFunc("/account/token", p.getToken).Methods("POST")
	r.HandleFunc("/account/update", p.auth(p.updateProfile)).Methods("POST")
	r.HandleFunc("/keys/types", p.auth(p.getKeyTypes))
	r.HandleFunc("/keys", p.auth(p.getAllKeys)).Methods("GET")
	r.HandleFunc("/keys", p.auth(p.createKey)).Methods("POST")
	r.HandleFunc("/keys/delete", p.auth(p.createKey)).Methods("POST")

	return r, nil
}
