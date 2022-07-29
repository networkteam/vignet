package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/apex/log"
	"github.com/apex/log/handlers/logfmt"
	"github.com/apex/log/handlers/text"
	"github.com/mattn/go-isatty"
	"github.com/open-policy-agent/opa/bundle"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	"github.com/networkteam/vignet"
	"github.com/networkteam/vignet/policy"
)

type ctxKey int

const (
	ctxKeyConfig ctxKey = iota
)

func main() {
	app := cli.NewApp()
	app.Name = "vignet"
	app.Usage = "The missing GitOps piece: expose Git repositories for automation via an authenticated HTTP API"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:     "address",
			Category: "http",
			Value:    ":8080",
			Usage:    "Address for HTTP server to listen on",
			EnvVars:  []string{"VIGNET_ADDRESS"},
		},
		&cli.PathFlag{
			Name:     "config",
			Category: "configuration",
			Aliases:  []string{"c"},
			Usage:    "Path to the configuration file",
			Value:    "config.yaml",
			EnvVars:  []string{"VIGNET_CONFIG"},
		},
		&cli.PathFlag{
			Name:     "policy",
			Category: "authorization",
			Usage:    "Path to an OPA policy bundle path, uses the built-in by default",
			EnvVars:  []string{"VIGNET_POLICY"},
		},
		&cli.BoolFlag{
			Name:     "verbose",
			Category: "logging",
			Usage:    "Enable verbose logging",
			EnvVars:  []string{"VIGNET_VERBOSE"},
		},
		&cli.BoolFlag{
			Name:     "force-logfmt",
			Category: "logging",
			Usage:    "Force logging to use logfmt",
			EnvVars:  []string{"VIGNET_FORCE_LOGFMT"},
		},
	}
	app.Before = func(c *cli.Context) error {
		if c.Bool("verbose") {
			log.SetLevel(log.DebugLevel)
		}
		setServerLogHandler(c)

		config, err := loadConfig(c.Path("config"))
		if err != nil {
			return err
		}
		c.Context = context.WithValue(c.Context, ctxKeyConfig, config)
		return nil
	}
	app.Description = "The default command starts the HTTP server that handles commands."
	app.Action = func(c *cli.Context) error {
		config := c.Context.Value(ctxKeyConfig).(vignet.Config)

		authenticationProvider, err := config.BuildAuthenticationProvider(c.Context)
		if err != nil {
			return fmt.Errorf("building authentication provider: %w", err)
		}
		switch config.AuthenticationProvider.Type {
		case vignet.AuthenticationProviderGitLab:
			log.
				WithField("gitlabUrl", config.AuthenticationProvider.GitLab.URL).
				Infof("Using GitLab authentication provider")
		default:
			log.Infof("Using authentication provider %s", config.AuthenticationProvider.Type)
		}

		authorizer, err := buildAuthorizer(c)
		if err != nil {
			return fmt.Errorf("building authorizer: %w", err)
		}

		h := vignet.NewHandler(authenticationProvider, authorizer, config)

		// TODO Add graceful shutdown
		log.WithField("address", c.String("address")).Infof("Starting HTTP server")
		err = http.ListenAndServe(c.String("address"), h)
		if err != nil {
			return fmt.Errorf("starting server: %w", err)
		}

		return nil
	}

	// TODO Add API to test authorization for commands

	err := app.Run(os.Args)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func loadConfig(configFilename string) (vignet.Config, error) {
	configFile, err := os.Open(configFilename)
	if err != nil {
		return vignet.Config{}, fmt.Errorf("opening config file: %w", err)
	}
	defer configFile.Close()

	config := vignet.DefaultConfig
	err = yaml.NewDecoder(configFile).Decode(&config)
	if err != nil {
		return vignet.Config{}, fmt.Errorf("decoding config file: %w", err)
	}
	err = config.Validate()
	if err != nil {
		return vignet.Config{}, fmt.Errorf("validating config file: %w", err)
	}
	return config, nil
}

func buildAuthorizer(c *cli.Context) (vignet.Authorizer, error) {
	var (
		b   *bundle.Bundle
		err error
	)

	if c.IsSet("policy") {
		policyPath := c.Path("policy")
		b, err = policy.LoadBundle(policyPath)
		if err != nil {
			return nil, fmt.Errorf("loading policy bundle: %w", err)
		}
		log.
			WithField("policyPath", policyPath).
			Infof("Loaded policy bundle")
	} else {
		b, err = policy.LoadDefaultBundle()
		if err != nil {
			return nil, fmt.Errorf("loading default bundle: %w", err)
		}
		log.Infof("Loaded default policy bundle")
	}

	return vignet.NewRegoAuthorizer(c.Context, b)
}

func setServerLogHandler(c *cli.Context) {
	isTerminal := isatty.IsTerminal(os.Stdout.Fd())
	if c.Bool("force-logfmt") || !isTerminal {
		log.SetHandler(logfmt.New(os.Stderr))
	} else {
		log.SetHandler(text.New(os.Stderr))
	}
}
