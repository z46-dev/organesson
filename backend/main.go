package main

import (
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/app"
	"github.com/z46-dev/organesson/backend/app/api"
	localauth "github.com/z46-dev/organesson/backend/auth"
	"github.com/z46-dev/organesson/backend/cli"
	"github.com/z46-dev/organesson/backend/config"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
)

// Note: any log.<level>f() method requires you to put a \n at the end of the string otherwise it will not go to a new line.
// Format methods allow you to be more specific.
var log *golog.Logger = golog.New().Prefix("[MAIN]", golog.BoldBlue).Timestamp()

func main() {
	var (
		arguments cli.Arguments
		parser    *arg.Parser
		err       error
	)
	if parser, err = cli.NewParser(&arguments); err != nil {
		log.Panicf("Failed to configure command line: %v\n", err)
	}
	if err = parser.Parse(os.Args[1:]); err != nil {
		if err == arg.ErrHelp {
			if arguments.Bootstrap != nil {
				if arguments.Bootstrap.ResetAdministratorPassword != nil {
					_ = parser.WriteHelpForSubcommand(os.Stdout, "bootstrap", "reset-administrator-password")
				} else {
					_ = parser.WriteHelpForSubcommand(os.Stdout, "bootstrap")
				}
			} else {
				parser.WriteHelp(os.Stdout)
			}
			return
		}
		if err == arg.ErrVersion {
			return
		}
		log.Panicf("Invalid command line: %v\n", err)
	}
	log.Info("Starting...")

	if err = config.Init(arguments.ConfigurationPath); err != nil {
		log.Panicf("Failed to load configuration: %v\n", err)
	}

	var store *db.Store
	if store, err = db.Open(config.Cfg.Database.File, log, arguments.AllowDestructiveMigrations); err != nil {
		log.Panicf("Failed to initialize database: %v\n", err)
	}
	defer store.Close()

	var authentication *localauth.Service
	if authentication, err = localauth.New(store); err != nil {
		log.Panicf("Failed to initialize authentication: %v\n", err)
	}

	if arguments.Bootstrap != nil && arguments.Bootstrap.ResetAdministratorPassword != nil {
		var token string
		if token, err = authentication.ResetInitialAdministratorLink(); err != nil {
			log.Panicf("Failed to create administrator password link: %v\n", err)
		}
		fmt.Printf("One-time administrator password link token: %s\n", token)
		return
	}

	var token string
	var created bool
	if token, created, err = authentication.EnsureInitialActivationLink(); err != nil {
		log.Panicf("Failed to initialize administrator activation: %v\n", err)
	}
	if created {
		log.Warningf("One-time administrator password setup token (expires in 30 minutes): %s\n", token)
		log.Warning("Redeem it through POST /api/v1/auth/bootstrap/redeem; see docs/first-vertical-slice.md")
	}

	var domainService *domain.Service = domain.New(store)
	var services api.Services = api.Services{Authentication: authentication, Domain: domainService, Store: store}
	if err = app.Start(services, config.Cfg.WebServer.Address, config.Cfg.WebServer.TLSDir, config.Cfg.WebServer.CORSAllowedOrigins); err != nil {
		log.Panicf("Failed to start web application: %v\n", err)
	}
}
