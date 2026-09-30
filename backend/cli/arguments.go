// Package cli contains argument definitions; command execution is intentionally not implemented yet.
package cli

import "github.com/alexflint/go-arg"

type (
	// Arguments defines the future Organesson administrative command-line interface.
	Arguments struct {
		ConfigurationPath string              `arg:"--config" default:"config.toml" help:"Path to the Organesson configuration file."`
		Bootstrap         *BootstrapArguments `arg:"subcommand:bootstrap" help:"Bootstrap administrative access."`
	}

	// BootstrapArguments groups host-local bootstrap actions.
	BootstrapArguments struct {
		ResetAdministratorPassword *ResetAdministratorPasswordArguments `arg:"subcommand:reset-administrator-password" help:"Create a new single-use administrator password-set link."`
	}

	// ResetAdministratorPasswordArguments reserves options for the reset command.
	ResetAdministratorPasswordArguments struct{}
)

// NewParser returns the shared parser without executing any command.
func NewParser(arguments *Arguments) (parser *arg.Parser, err error) {
	parser, err = arg.NewParser(arg.Config{}, arguments)

	return
}
