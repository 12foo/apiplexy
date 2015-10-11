package apiplexy

import (
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/garyburd/redigo/redis"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"time"
)

// Rate limiting is handled by an exponentially weighted moving
// average calculation that's done inside Redis. This is the lua code
// for that calculaton; it's loaded into Redis on startup.
//
// http://www-uxsup.csx.cam.ac.uk/~fanf2/hermes/doc/antiforgery/ratelimit-demo.html
const ewmaScript = `
    local kts, kavg = unpack(KEYS)
    local now, max, period, cost = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3]), tonumber(ARGV[4])

    local last = redis.call('GET', kts)
    local avg, dt

    if last ~= false then
        avg = redis.call('GET', kavg)
        if avg == false then avg = 0 else avg = tonumber(avg) end
        dt = now - tonumber(last)
    else
        avg = 0
        dt = period
    end
    if dt == 0 then dt = 1 end

    local a = math.exp(-dt/period)
    local rate = cost * period / dt
    avg = (1 - a) * rate + a * avg

    if avg > max then
        return 1
    else
        local expire = period * 2
        redis.call('SETEX', kts, expire, now)
        redis.call('SETEX', kavg, expire, avg)
        return 0
    end
`

type apiplexPluginInfo struct {
	Name        string
	Description string
	Link        string
	pluginType  reflect.Type
}

var registeredPlugins = make(map[string]apiplexPluginInfo)

type APIUpstream struct {
	Client  *http.Client
	Address *url.URL
}

type apiplex struct {
	signingKey    string
	email         apiplexConfigEmail
	lastAlert     *time.Time
	upstreams     []APIUpstream
	apipath       string
	authCacheMins int
	quotas        map[string]apiplexQuota
	allowKeyless  bool
	ewmaScript    *redis.Script
	redis         *redis.Pool
	auth          []AuthPlugin
	backends      []BackendPlugin
	usermgmt      ManagementBackendPlugin
	postauth      []PostAuthPlugin
	preupstream   []PreUpstreamPlugin
	postupstream  []PostUpstreamPlugin
	logging       []LoggingPlugin
	startables    []LifecyclePlugin
}

// RegisterPlugin makes your plugin available to apiplexy. You should probably
// call this from the init() function of your plugin file, so your plugin
// works as a silent import. Name is the plugin's unique name (lowercase).
// Description is a one-sentence description of what your plugin does. The
// link should lead to a documentation webpage about your plugin (or your
// github repo with a README). For the plugin parameter, pass a zero-value
// instance of your plugin struct, i.e. YourPlugin{}.
func RegisterPlugin(name, description, link string, plugin interface{}) {
	registeredPlugins[name] = apiplexPluginInfo{
		Name:        name,
		Description: description,
		Link:        link,
		pluginType:  reflect.TypeOf(plugin),
	}
}

// AvailablePlugins gets a map of currently registered plugins.
func AvailablePlugins() map[string]apiplexPluginInfo {
	return registeredPlugins
}

// ExampleConfiguration generates an ApiplexConfig struct with example values
// and the specified plugins inserted with their default configurations at
// their appropriate places in the plugin tree. This is a good starting point
// to give to the user for customization.
func ExampleConfiguration(pluginNames []string) (*ApiplexConfig, error) {
	c := ApiplexConfig{
		Redis: apiplexConfigRedis{
			Host: "127.0.0.1",
			Port: 6379,
			DB:   0,
		},
		Email: apiplexConfigEmail{
			AlertsTo:       []string{"your@email.com"},
			AlertsCooldown: 30,
			From:           "Your API <noreply@your-api.com>",
			Server:         "localhost",
			Port:           25,
		},
		Quotas: map[string]apiplexQuota{
			"default": apiplexQuota{
				Minutes: 5,
				MaxIP:   50,
				MaxKey:  5000,
			},
			"keyless": apiplexQuota{
				Minutes: 5,
				MaxIP:   20,
			},
		},
		Serve: apiplexConfigServe{
			Port:       5000,
			API:        "/",
			Upstreams:  []string{"http://your-actual-api:8000/"},
			PortalAPI:  "/portal-api/",
			SigningKey: uniuri.NewLen(64),
		},
	}
	plugins := apiplexConfigPlugins{}
	for _, pname := range pluginNames {
		pInfo, ok := registeredPlugins[pname]
		if !ok {
			return nil, fmt.Errorf("No plugin '%s' available.", pname)
		}

		pluginPtr := reflect.New(pInfo.pluginType)
		defConfig := pluginPtr.MethodByName("DefaultConfig").Call([]reflect.Value{})[0].Interface().(map[string]interface{})
		pconfig := apiplexPluginConfig{Plugin: pname, Config: defConfig}

		switch pluginPtr.Interface().(type) {
		case AuthPlugin:
			plugins.Auth = append(plugins.Auth, pconfig)
		case ManagementBackendPlugin:
			plugins.Backend = append(plugins.Backend, pconfig)
		case BackendPlugin:
			plugins.Backend = append(plugins.Backend, pconfig)
		case PreUpstreamPlugin:
			plugins.PreUpstream = append(plugins.PreUpstream, pconfig)
		case PostUpstreamPlugin:
			plugins.PostUpstream = append(plugins.PostUpstream, pconfig)
		case PostAuthPlugin:
			plugins.PostAuth = append(plugins.PostAuth, pconfig)
		case LoggingPlugin:
			plugins.Logging = append(plugins.Logging, pconfig)
		}
	}
	c.Plugins = plugins
	return &c, nil
}

func ensureDefaults(target map[string]interface{}, defaults map[string]interface{}) error {
	for dk, dv := range defaults {
		defaultType := reflect.TypeOf(dv)
		if tv, ok := target[dk]; ok {
			if reflect.TypeOf(tv) != defaultType {
				return fmt.Errorf("Field '%s': expected a value of type %T.", dk, dv)
			}
			defaultZero := reflect.New(defaultType)
			if tv == defaultZero {
				target[dk] = dv
			}
		} else {
			target[dk] = dv
		}
	}
	return nil
}

// A little black magic here: buildPlugins uses reflection to reify and configure
// actual working plugins from zero-value references. The plugins are also reflect-
// typechecked so we don't run into nasty surprises later.
func buildPlugins(plugins []apiplexPluginConfig, pluginType reflect.Type, startables []LifecyclePlugin) ([]interface{}, error) {
	built := make([]interface{}, len(plugins))
	for i, config := range plugins {
		ptype, ok := registeredPlugins[config.Plugin]
		if !ok {
			return nil, fmt.Errorf("No plugin named '%s' available.", config.Plugin)
		}
		pt := reflect.New(ptype.pluginType)

		if !pt.Type().Implements(pluginType) {
			return nil, fmt.Errorf("Plugin '%s' (%s) cannot be loaded as %s.", config.Plugin, ptype.pluginType.Name(), pluginType.Name())
		}

		defConfig := pt.MethodByName("DefaultConfig").Call([]reflect.Value{})[0].Interface().(map[string]interface{})
		if err := ensureDefaults(config.Config, defConfig); err != nil {
			return nil, fmt.Errorf("While configuring '%s': %s", config.Plugin, err.Error())
		}
		maybeErr := pt.MethodByName("Configure").Call([]reflect.Value{reflect.ValueOf(config.Config)})[0].Interface()
		if maybeErr != nil {
			err := maybeErr.(error)
			return nil, fmt.Errorf("While configuring '%s': %s", config.Plugin, err.Error())
		}

		st := pt.Interface()
		built[i] = st

		if pt.Type().Implements(reflect.TypeOf((*LifecyclePlugin)(nil)).Elem()) {
			lt := st.(LifecyclePlugin)
			startables = append(startables, lt)
		}
	}
	return built, nil
}

// Helper method so all HTTP paths in the configuration have a final slash
// (less uncertainty about path matching).
func ensureFinalSlash(s string) string {
	if s[len(s)-1] != '/' {
		return s + "/"
	} else {
		return s
	}
}

// constructs an Apiplex, i.e. an apiplexy struct that can run plugins on
// requests and proxy them back to one or more upstream backends.
func buildApiplex(config ApiplexConfig) (*apiplex, error) {
	if config.Serve.API == "" {
		config.Serve.API = "/"
	}

	if len(config.Email.AlertsTo) == 0 {
		return nil, fmt.Errorf("You must define at least one recipient for error alert emails.")
	}

	if config.Serve.SigningKey == "" {
		config.Serve.SigningKey = uniuri.NewLen(64)
	}

	// TODO make everything configurable
	ap := apiplex{
		apipath:       ensureFinalSlash(config.Serve.API),
		authCacheMins: 10,
		signingKey:    config.Serve.SigningKey,
		email:         config.Email,
		lastAlert:     nil,
	}

	if _, ok := config.Quotas["default"]; !ok {
		return nil, fmt.Errorf("Your configuration must specify at least a 'default' quota.")
	}
	if kl, ok := config.Quotas["keyless"]; ok {
		if kl.MaxKey != 0 {
			return nil, fmt.Errorf("You cannot set a per-key maximum for the 'keyless' quota.")
		}
		ap.allowKeyless = true
	} else {
		ap.allowKeyless = false
	}
	ap.quotas = config.Quotas

	// this slice will contain all plugins that implement LifecyclePlugin after all plugins
	// are configured
	ap.startables = []LifecyclePlugin{}

	// auth plugins
	auth, err := buildPlugins(config.Plugins.Auth, reflect.TypeOf((*AuthPlugin)(nil)).Elem(), ap.startables)
	if err != nil {
		return nil, err
	}
	ap.auth = make([]AuthPlugin, len(auth))
	for i, p := range auth {
		cp := p.(AuthPlugin)
		ap.auth[i] = cp
	}

	// backend plugins
	backend, err := buildPlugins(config.Plugins.Backend, reflect.TypeOf((*BackendPlugin)(nil)).Elem(), ap.startables)
	if err != nil {
		return nil, err
	}
	ap.backends = make([]BackendPlugin, len(backend))
	for i, p := range backend {
		cp := p.(BackendPlugin)
		ap.backends[i] = cp
	}

	// The first ManagementBackendPlugin (i.e. one with additional user/key management) gets special
	// treatment: if the portal API is enabled, it will connect directly to this plugin and use that
	// to perform portal actions.
	for _, plugin := range ap.backends {
		// must use reflection here since type switch will see ap.backends as implementing
		// BackendPlugin only
		if reflect.TypeOf(plugin).Implements(reflect.TypeOf((*ManagementBackendPlugin)(nil)).Elem()) {
			mgmt := plugin.(ManagementBackendPlugin)
			ap.usermgmt = mgmt
			break
		}
	}

	// postauth plugins
	postauth, err := buildPlugins(config.Plugins.PostAuth, reflect.TypeOf((*PostAuthPlugin)(nil)).Elem(), ap.startables)
	if err != nil {
		return nil, err
	}
	ap.postauth = make([]PostAuthPlugin, len(postauth))
	for i, p := range postauth {
		cp := p.(PostAuthPlugin)
		ap.postauth[i] = cp
	}

	// preupstream plugins
	preupstream, err := buildPlugins(config.Plugins.PreUpstream, reflect.TypeOf((*PreUpstreamPlugin)(nil)).Elem(), ap.startables)
	if err != nil {
		return nil, err
	}
	ap.preupstream = make([]PreUpstreamPlugin, len(preupstream))
	for i, p := range preupstream {
		cp := p.(PreUpstreamPlugin)
		ap.preupstream[i] = cp
	}

	// postupstream plugins
	postupstream, err := buildPlugins(config.Plugins.PostUpstream, reflect.TypeOf((*PostUpstreamPlugin)(nil)).Elem(), ap.startables)
	if err != nil {
		return nil, err
	}
	ap.postupstream = make([]PostUpstreamPlugin, len(postupstream))
	for i, p := range postupstream {
		cp := p.(PostUpstreamPlugin)
		ap.postupstream[i] = cp
	}

	// logging plugins
	logging, err := buildPlugins(config.Plugins.Logging, reflect.TypeOf((*LoggingPlugin)(nil)).Elem(), ap.startables)
	if err != nil {
		return nil, err
	}
	ap.logging = make([]LoggingPlugin, len(logging))
	for i, p := range logging {
		cp := p.(LoggingPlugin)
		ap.logging[i] = cp
	}

	// upstreams
	ap.upstreams = make([]APIUpstream, len(config.Serve.Upstreams))
	for i, us := range config.Serve.Upstreams {
		u, err := url.Parse(us)
		if err != nil {
			return nil, fmt.Errorf("Invalid upstream address: %s", us)
		}
		ap.upstreams[i] = APIUpstream{
			Client:  &http.Client{},
			Address: u,
		}
	}

	ap.redis = &redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", config.Redis.Host+":"+strconv.Itoa(config.Redis.Port))
			if err != nil {
				return nil, err
			}
			c.Do("SELECT", config.Redis.DB)
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}

	// test connection (by loading script)
	rd := ap.redis.Get()
	ap.ewmaScript = redis.NewScript(2, ewmaScript)
	err = ap.ewmaScript.Load(rd)
	if err != nil {
		log.Fatalf("Couldn't connect to Redis. %s", err.Error())
	}

	for _, st := range ap.startables {
		err := st.Start(ap.reportError)
		if err != nil {
			log.Fatalf("Error starting plugin. %s", err.Error())
		}
	}

	return &ap, nil
}

func (ap *apiplex) Shutdown() {
	for _, st := range ap.startables {
		err := st.Stop()
		if err != nil {
			log.Printf("Error stopping plugin. %s\n", err.Error())
		}
	}
}

func New(config ApiplexConfig) (*http.ServeMux, error) {
	ap, err := buildApiplex(config)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc(ap.apipath, ap.HandleAPI)

	if config.Serve.PortalAPI != "" {
		papath := ensureFinalSlash(config.Serve.PortalAPI)
		portalAPI, err := ap.BuildPortalAPI(config.Serve.PortalAPI)
		if err != nil {
			return nil, fmt.Errorf("Could not create Portal API. %s", err.Error())
		}
		mux.Handle(papath, portalAPI)
	}

	return mux, nil
}
