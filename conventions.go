package apiplexy

import (
	"net/http"
)

// If your plugin returns an AbortRequest as its error value, the API request
// will be aborted and the error message routed through to the client. Use this
// to return custom 403 errors, for example. If you forget to set a status,
// apiplexy will set 400.
//
// If your plugin returns a plain error, apiplexy assumes something went wrong
// internally, and returns a generic Error 500 to the client.
type AbortRequest struct {
	Status  int
	Message string
}

func (e AbortRequest) Error() string {
	return e.Message
}

// Abort is a utility function to quickly whip up an AbortRequest.
func Abort(status int, message string) AbortRequest {
	return AbortRequest{Status: status, Message: message}
}

// various structs used for config parsing; not really helpful to have public
type apiplexPluginConfig struct {
	Plugin string
	Config map[string]interface{} `yaml:",omitempty" json:",omitempty"`
}

type apiplexConfigRedis struct {
	Host string
	Port int
	DB   int
}

type apiplexConfigServe struct {
	Port       int
	Backends   map[string][]string
	Static     map[string]string
	PortalAPI  string `yaml:"portal_api"`
	SigningKey string `yaml:"signing_key"`
}

type apiplexConfigPlugins struct {
	Auth         []apiplexPluginConfig `yaml:",omitempty" json:",omitempty"`
	Backend      []apiplexPluginConfig `yaml:",omitempty" json:",omitempty"`
	PostAuth     []apiplexPluginConfig `yaml:",omitempty" json:",omitempty"`
	PreUpstream  []apiplexPluginConfig `yaml:",omitempty" json:",omitempty"`
	PostUpstream []apiplexPluginConfig `yaml:",omitempty" json:",omitempty"`
	Logging      []apiplexPluginConfig `yaml:",omitempty" json:",omitempty"`
}

type apiplexConfigEmail struct {
	AlertsTo       []string `yaml:"alerts_to"`
	AlertsCooldown int      `yaml:"alerts_cooldown"`
	From           string
	Server         string
	Port           int
	User           string
	Password       string
}

type apiplexQuota struct {
	Minutes int `json:"minutes"`
	MaxIP   int `json:"max_ip,omitempty" yaml:"max_ip,omitempty"`
	MaxKey  int `json:"max_key,omitempty" yaml:"max_key,omitempty"`
}

type ApiplexConfig struct {
	Redis   apiplexConfigRedis
	Email   apiplexConfigEmail
	Quotas  map[string]apiplexQuota
	Serve   apiplexConfigServe
	Plugins apiplexConfigPlugins
}

// User represents a user (or developer) who can create and use keys in their
// app. Users are uniquely identified by their emails, and that's really all
// the management plugins need. You are free to put additional profile data
// into the Data field, as long as it can be serialized to JSON.
type User struct {
	Email   string                 `json:"email"`
	Name    string                 `json:"name"`
	Active  bool                   `json:"active"`
	Profile map[string]interface{} `json:"profile,omitempty"`
}

// A Key has a unique ID, a user-defined Type (like "HMAC"), an assigned Quota
// and can have extra data (such as secret signing keys) attached for validation.
//
// The key's Realm is either an app identifier (for native apps) or a web domain.
// If apiplexy receives a request with a Referrer header set (meaning it came from
// a web app), it will check the webapp's Referrer domain against the key's Realm.
//
// The key's owner is an email address (hopefully found in one of the backing stores.
// Keys do not require an owner, but ownerless keys don't trigger any quota overage
// notifications (for obvious reasons).
type Key struct {
	ID    string                 `json:"id"`
	Realm string                 `json:"realm"`
	Quota string                 `json:"quota"`
	Type  string                 `json:"type"`
	Owner string                 `json:"-"`
	Data  map[string]interface{} `json:"data,omitempty"`
}

// An APIContext map accompanies every API request through its lifecycle. Use this
// to store data that will be available to plugins down the chain.
//
// As a convention, Logging plugins MUST log everything stored under Log. Log MUST
// at least(!) be kept JSON-serializable; or better yet, as a map from strings to
// plain types.
type APIContext struct {
	Keyless  bool
	Key      *Key
	Cost     int
	Path     string
	Upstream *APIUpstream
	DoNotLog bool
	Log      map[string]interface{}
	Data     map[string]interface{}
}

// Description of a key type that an AuthPlugin may offer.
type KeyType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// A basic ApiplexPlugin is always initialized as a zero value. The builder
// then calls Configure, supplying user configuration.
//
// Configure is passed a configuration that has been supplied by the user. At
// the end of Configure, your plugin should be fully ready to run. Returning
// an error will abort apiplexy's startup completely with the error message.
//
// DefaultConfig should return a default configuration map for your plugin.
// This does not need to be a configuration that works imemdiately, but your
// plugin's Configure methos must take these defaults without panicking, and
// return sensible error messages.
//
// The builder ensures that before any configuration is passed to your
// Configure method, it has all the keys in your DefaultConfig, with their
// values type-matching the default values.
type Plugin interface {
	Configure(config map[string]interface{}) error
	DefaultConfig() map[string]interface{}
}

// Plugins that implement LifecyclePlugin will be started and stopped in an
// orderly fashion by the API gateway. Start() is called after Configure()
// and receives a function that can be used to report errors during the
// plugin's runtime. This interface is useful for plugins that keep their
// own goroutines running in the background.
//
// Stop() is called when the API gateway shuts down.
type LifecyclePlugin interface {
	Start(report func(error)) error
	Stop() error
}

// An AuthPlugin takes responsibility for one or several authentication methods
// that an API request may use. You might have an auth plugin for HMAC, one
// for OAuth2, and so on.
//
// Detect receives the incoming request. The plugin analyzes the request and
// checks whether it contains authentication bits (like a header or parameter)
// that it recognizes. From there it works out a string that is probably the
// key, and a key type (like HMAC, Token, and so on). That information is then
// tried on the various backends, until one finds the key in its key store.
//
// After some backend has found the full key, it is sent back to the auth
// plugin's Validate function for validation against the request. If simply finding
// the key is validation enough, just return true here. For HMAC, for example, you
// would verify the key by checking the request signature against the secret key
// retrieved from the backend.
type AuthPlugin interface {
	Plugin
	AvailableTypes() []KeyType
	Generate(keyType string) (key Key, err error)
	Detect(req *http.Request, ctx *APIContext) (maybeKey string, keyType string, authCtx map[string]interface{}, err error)
	Validate(key *Key, req *http.Request, ctx *APIContext, authCtx map[string]interface{}) (isValid bool, err error)
}

// A basic BackendPlugin can retrieve valid keys from some sort of key store.
// It can not delete or manage these keys, and is used exclusively in request
// authentication.
type BackendPlugin interface {
	Plugin
	GetKey(keyID string, keyType string) (*Key, error)
}

// A ManagementBackendPlugin supports full user and key management inside
// some kind of backing store. If you configure one of these, the portal API
// can use it to build an instant developer self-service portal.
//
// Most of these functions should be fairly self-explanatory. Some extra hints:
//
// AddUser gets passed a preliminary user struct reference. It MUST overwrite
// the *User's email and password with the values passed as arguments before
// saving the new user to your backend store.
//
// Normally, the portal API configuration decides whether a user needs email
// verification before the account is activated (by passing in the preliminary user
// with the user.Active bit set accordingly). It's best to leave this alone.
// However, your backend plugin MAY override this part by setting user.Active
// before storing/returning the saved user. If user.Active is false on the returned
// user, the portal API will automatically perform email verification.
//
// UpdateUser MUST NOT overwrite the user's email or password.
type ManagementBackendPlugin interface {
	BackendPlugin
	AddUser(email string, password string, user *User) error
	GetUser(email string) *User
	Authenticate(email string, password string) *User
	ActivateUser(email string) error
	ResetPassword(email string, newPassword string) error
	UpdateUser(email string, user *User) error
	AddKey(email string, key *Key) error
	DeleteKey(email string, keyID string) error
	GetAllKeys(email string) ([]*Key, error)
}

// A plugin that runs immediately after authentication (so the request is valid
// and generally allowed), but before the quota is checked. Use this to restrict
// access or modify cost based on things like the request's path. apiplexy checks
// the context's "cost" entry during quota calculations.
//
//  ctx.Cost = 3
type PostAuthPlugin interface {
	Plugin
	PostAuth(req *http.Request, ctx *APIContext) error
}

// A PreUpstreamPlugin runs after the quota has been checked and applied, but before
// the request is going ahead to upstream. As the user has already "paid" quota at this
// point, it's important that you avoid aborting the request unless there's a critical
// reason. Prefer a PostAuthPlugin for likely aborts.
type PreUpstreamPlugin interface {
	Plugin
	PreUpstream(req *http.Request, ctx *APIContext) error
}

// A PostUpstreamPlugin runs after the request has been handled by upstream, and
// receives an additional "res" parameter. This is the response returned by upstream.
// You can modify the response body here.
type PostUpstreamPlugin interface {
	Plugin
	PostUpstream(req *http.Request, res *http.Response, ctx *APIContext) error
}

// LoggingPlugins are run after the main request has already completed and the response
// has been sent back to the user. Modifying the response will have no effect. This
// stage is (as the name implies) best suited for logging plugins.
//
// As a convention, logging plugins MUST log/store all entries in the ctx.Log map. This
// map is, also by convention, always JSON-serializable.
type LoggingPlugin interface {
	Plugin
	Log(req *http.Request, res *http.Response, ctx *APIContext) error
}
