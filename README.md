# apiplexy

An API gateway / management swiss army knife for pissed-off engineers who need
things to work yesterday, using stuff they have already lying around.

## Features/Non-Features

  * You only need to have Redis running, everything else is optional.
  * Does access control, (smooth) rate limiting, reverse proxying and basic
    error reporting at its core, plus lots of other stuff via plugins. Try
    `apiplexy plugins` for a list.
  * Allows multiple different per-key and per-IP quotas, plus a 'keyless'
    quota for giving people a taste.
  * No fancy GUI. The complete configuration is in one YAML file.
  * `apiplexy gen-conf [plugin-name...]` generates a skeleton configuration for you.
    Just modify and run.
  * Doesn't do its own stats. Use a plugin to log to your existing stats services,
    like InfluxDB, Logstash or Graphite.
  * Doesn't bring its own user/key backing store. Use a plugin to connect to
    existing databases like MongoDB, SQLite, Postgres, MySQL, and so on.
  * Move easily from your previous API management system by connecting it as your
    second (or third...) backing store. Old keys will just keep working.
  * apiplexy doesn't do exactly what you need? Use Lua scripting straight from
    your config, or extend apiplexy by writing your own Go plugins (pull requests
    welcome).

### Developer Portal/Frontend

The same philosophy extends to the frontend: apiplexy doesn't prescribe Swagger, RAML,
Blueprint or what-have-you for your frontend and docs. Those all have great documentation
browsers already that you can just use.

But developers need a place where they can create and manage their keys, or maybe
read up on the docs, right? No problem. Just hook up to your backing store using one of
the "full management" plugins, and apiplexy automatically exposes it via a portal API.

Download/clone [apiplexy-portal](https://github.com/12foo/apiplexy-portal), edit index.html
and connect it to your portal API. Instant frontend. You can write your own too, but if you
just need to put some info up real quick, the portal comes with a built-in renderer for
markdown pages.

## Contributing / Building

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

