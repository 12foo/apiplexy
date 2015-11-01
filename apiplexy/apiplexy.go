package main

// Import apiplexy plugins in a separate block (just because it looks nicer).
import (
	_ "github.com/12foo/apiplexy/auth/hmac"
	_ "github.com/12foo/apiplexy/backend/sql"
	_ "github.com/12foo/apiplexy/logging"
	_ "github.com/12foo/apiplexy/misc"
	"time"
)

import (
	"fmt"
	"github.com/12foo/apiplexy"
	"github.com/codegangsta/cli"
	"github.com/fvbock/endless"
	"github.com/skratchdot/open-golang/open"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"
)

func listPlugins(c *cli.Context) {
	fmt.Printf("Available plugins:\n\n")
	avail := apiplexy.AvailablePlugins()
	pnames := make([]string, len(avail))
	i := 0
	for n, _ := range avail {
		pnames[i] = n
		i++
	}
	sort.Strings(pnames)

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '\t', 0)
	for _, name := range pnames {
		plugin := avail[name]
		fmt.Fprintf(w, "   %s\t %s\n", name, plugin.Description)
	}
	fmt.Fprintln(w)
	w.Flush()
}

func docPlugin(c *cli.Context) {
	if len(c.Args()) != 1 {
		fmt.Printf("Which documentation do you want to open? Try 'apiplexy plugin-doc <plugin-name>'.\n")
		os.Exit(1)
	}
	plugin, ok := apiplexy.AvailablePlugins()[c.Args()[0]]
	if !ok {
		fmt.Printf("Plugin '%s' not found. Try 'apiplexy plugins' to list available ones.\n", c.Args()[0])
		os.Exit(1)
	}
	fmt.Printf("Opening documentation for '%s' at: %s\n", plugin.Name, plugin.Link)
	open.Start(plugin.Link)
}

func generateConfig(c *cli.Context) {
	config, err := apiplexy.ExampleConfiguration(c.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't generate configuration: %s\n", err.Error())
		os.Exit(1)
	}
	yml, err := yaml.Marshal(&config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't generate configuration: %s\n", err.Error())
		os.Exit(1)
	}
	os.Stdout.Write(yml)
}

func start(c *cli.Context) {
	configPath := c.String("config")
	yml, err := ioutil.ReadFile(os.ExpandEnv(configPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't read config file: %s\n", err.Error())
		os.Exit(1)
	}
	config := apiplexy.ApiplexConfig{}
	err = yaml.Unmarshal(yml, &config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't parse configuration: %s\n", err.Error())
		os.Exit(1)
	}

	ap, err := apiplexy.New(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't initialize API proxy. %s\n", err.Error())
		os.Exit(2)
	}

	fmt.Printf("Running server on port %d.\n", config.Serve.Port)

	endless.DefaultReadTimeOut = 10 * time.Second
	endless.DefaultWriteTimeOut = 10 * time.Second
	endless.ListenAndServe("0.0.0.0:"+strconv.Itoa(config.Serve.Port), ap)
}

func main() {
	app := cli.NewApp()
	app.Name = "apiplexy"
	app.Usage = "Pluggable API gateway/proxy system."
	app.Commands = []cli.Command{
		{
			Name:    "plugins",
			Usage:   "Lists available apiplexy plugins",
			Aliases: []string{"ls"},
			Action:  listPlugins,
		},
		{
			Name:    "plugin-doc",
			Usage:   "Opens documentation webpage for a plugin",
			Aliases: []string{"doc"},
			Action:  docPlugin,
		},
		{
			Name:    "gen-conf",
			Usage:   "Generates a configuration file with the specified plugins",
			Aliases: []string{"gen"},
			Action:  generateConfig,
		},
		{
			Name:   "start",
			Usage:  "Starts API proxy using specified config file",
			Action: start,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "config, c",
					Value: "apiplexy.yaml",
					Usage: "Location of configuration file",
				},
			},
		},
	}
	app.Run(os.Args)
}
