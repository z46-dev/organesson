package cli

import (
	"testing"

	"github.com/alexflint/go-arg"
)

// TestResetAdministratorPasswordCommandParses checks the documented host-local command shape.
func TestResetAdministratorPasswordCommandParses(t *testing.T) {
	var arguments Arguments
	var parser *arg.Parser
	var err error
	if parser, err = NewParser(&arguments); err != nil {
		t.Fatalf("create parser: %v", err)
	}
	if err = parser.Parse([]string{"--config", "test.toml", "bootstrap", "reset-administrator-password"}); err != nil {
		t.Fatalf("parse reset command: %v", err)
	}
	if arguments.ConfigurationPath != "test.toml" || arguments.Bootstrap == nil || arguments.Bootstrap.ResetAdministratorPassword == nil {
		t.Fatalf("unexpected parsed arguments: %#v", arguments)
	}
}
