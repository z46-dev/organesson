// Package cli contains the host-local administrative command-line interface.
package cli

import "github.com/alexflint/go-arg"

type (
	// Arguments defines the future Organesson administrative command-line interface.
	Arguments struct {
		ConfigurationPath          string                `arg:"--config" default:"config.toml" help:"Path to the Organesson configuration file."`
		AllowDestructiveMigrations bool                  `arg:"--allow-destructive-migrations" help:"Allow database migrations that drop or rebuild columns."`
		Bootstrap                  *BootstrapArguments   `arg:"subcommand:bootstrap" help:"Bootstrap administrative access."`
		Development                *DevelopmentArguments `arg:"subcommand:development" help:"Development-only helper commands."`
	}

	// BootstrapArguments groups host-local bootstrap actions.
	BootstrapArguments struct {
		ResetAdministratorPassword *ResetAdministratorPasswordArguments `arg:"subcommand:reset-administrator-password" help:"Create a new single-use administrator password-set link."`
	}

	// DevelopmentArguments groups explicitly enabled development-only commands.
	DevelopmentArguments struct {
		SeedTestUsers *SeedTestUsersArguments `arg:"subcommand:seed-test-users" help:"Create Alice, Bob, Charlie, and Dave local test accounts."`
	}

	// SeedTestUsersArguments contains no additional options.
	SeedTestUsersArguments struct{}

	// ResetAdministratorPasswordArguments contains options for the reset command.
	ResetAdministratorPasswordArguments struct{}
)

// NewParser returns the shared parser without executing any command.
func NewParser(arguments *Arguments) (parser *arg.Parser, err error) {
	parser, err = arg.NewParser(arg.Config{}, arguments)

	return
}
