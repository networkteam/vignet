package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/apex/log"
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
			Name:  "address",
			Value: ":8080",
			Usage: "Address for HTTP server to listen on",
		},
		&cli.PathFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to the configuration file",
			Value:   "config.yaml",
		},
		&cli.PathFlag{
			Name:  "policy",
			Usage: "Path to an OPA policy bundle path, use the built-in by default",
		},
	}
	app.Before = func(c *cli.Context) error {
		// TODO Add configurable log level
		log.SetLevel(log.DebugLevel)

		configFile, err := os.Open(c.Path("config"))
		if err != nil {
			return fmt.Errorf("opening config file: %w", err)
		}
		defer configFile.Close()

		var config vignet.Config
		err = yaml.NewDecoder(configFile).Decode(&config)
		if err != nil {
			return fmt.Errorf("decoding config file: %w", err)
		}
		err = config.Validate()
		if err != nil {
			return fmt.Errorf("validating config file: %w", err)
		}

		c.Context = context.WithValue(c.Context, ctxKeyConfig, config)

		return nil
	}
	app.Action = func(c *cli.Context) error {
		config := c.Context.Value(ctxKeyConfig).(vignet.Config)

		authenticationProvider, err := config.BuildAuthenticationProvider(c.Context)
		if err != nil {
			return fmt.Errorf("building authentication provider: %w", err)
		}

		authorizer, err := buildAuthorizer(c)
		if err != nil {
			return fmt.Errorf("building authorizer: %w", err)
		}

		h := vignet.NewHandler(authenticationProvider, authorizer)

		// TODO Add graceful shutdown
		log.Infof("Starting HTTP server on %s", c.String("address"))
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

func buildAuthorizer(c *cli.Context) (vignet.Authorizer, error) {
	var (
		b   *bundle.Bundle
		err error
	)

	if c.IsSet("policy") {
		policyPath := c.Path("policy")
		log.Infof("Loading policy bundle from %s", policyPath)
		b, err = policy.LoadBundle(policyPath)
	} else {
		b, err = policy.LoadDefaultBundle()
		if err != nil {
			return nil, fmt.Errorf("loading default bundle: %w", err)
		}
	}

	return vignet.NewRegoAuthorizer(c.Context, b)
}
