# apiplexy ![Travis CI](https://travis-ci.org/12foo/apiplexy.svg?branch=master)

An API gateway / management swiss army knife for disgruntled nerds who need
things to work yesterday, using stuff they have already lying around.

## Features

  * Requires only Redis to run (everything else is a plugin).
  * All configuration is in one YAML text file. The CLI can generate one to get
    you started.
  * Serves both static files and backend APIs (including simple load balancing).
  * Supports multiple usage quotas, including a keyless one for free testing.
  * Authenticate using HMAC, OAuth2, HTTP PLAIN or write your own scheme.
  * Keep your keys, users and traffic stats where you like (most popular SQL
    databases, InfluxDB, ElasticSearch, mongoDB + pull requests welcome).
  * You can use multiple user/key backends to support authentication from multiple
    sources, making switchovers painless.
  * Lua scripting right in the config for small tweaks.
  * Easy to customize by writing Go plugins and recompiling if scripting isn't
    enough for you.

### How is apiplexy different from...

  * **[Tyk](http://tyk.io/)**: apiplexy doesn't come with a fancy web frontend
    (actually, I prefer text-based configuration. For a *user* frontend, see
    below). apiplexy also doesn't force you to do everything, including API
    stats, in mongoDB.
  * **[Kong](http://https://getkong.org/)**: apiplexy doesn't require you to
    set up OpenResty, Lua, and a Cassandra cluster. apiplexy also isn't really
    intended for managing multiple entirely separate APIs, but you can do that
    with clever use of quotas and a simple Lua script if you really want to.

Note that either of these may still be a better fit for you than apiplexy; I encourage
you to check them out.

### Developer Portal/Frontend

apiplexy doesn't come with a built-in frontend for users, and especially not
one that displays the API documentation. There's a bunch of different API docs
frameworks around (Swagger, RAML, Blueprint...) that already have great
documentation browsers. Just use what you like, and serve the documentation
pages using apiplexy's static paths.

For user/key management, just make sure that one of your backend plugins
supports FULL user management (this is noted in the plugin docs). If so,
apiplexy will use that plugin to expose a REST API with management functions
(the 'portal API').

Download/clone [apiplexy-portal](https://github.com/12foo/apiplexy-portal),
edit index.html and connect it to your portal API. Serve it on one of
apiplexy's static paths: instant developer portal. You can write your own too,
but if you just need to put some info up real quick, the portal comes with a
built-in renderer for markdown pages.

## Contributing / Building

Pull requests are very welcome, especially ones that allow you to hook apiplexy
up to more things more easily. Pull requests should be accompanied by tests.

### Note on building for production

apiplexy's Lua plugin can be built with normal Lua 5.1 or LuaJIT. If you use
the Lua plugin in production, LuaJIT is recommended for performance. To set
up your build accordingly, rebuild the 'golua' dependency for LuaJIT by doing

```bash
$ CGO_CFLAGS=`pkg-config luajit --cflags`
$ CGO_LDFLAGS=`pkg-config luajit --libs`
$ go get -f -u github.com/aarzilli/golua/lua
```

before you build.

### License

apiplexy is licensed under MIT, with a **special restriction**: you may not
sell apiplexy itself as a SaaS service. Meaning, you can and should run your
own commercial/paid services behind apiplexy, but you can't sell apiplexy
itself as a service to become the next 3scale/Apigee/etc. For details, see the
LICENSE file.
