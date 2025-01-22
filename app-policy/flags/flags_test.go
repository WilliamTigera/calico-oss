package flags_test

import (
	"fmt"
	"testing"

	"github.com/projectcalico/calico/app-policy/flags"
)

// TestFlagDefaults tests that the flags' defaults are set correctly.
func TestFlagDefaults(t *testing.T) {
	args := []string{"dikastes", "server"}
	config := flags.New()
	if err := config.Parse(args); err != nil {
		t.Errorf("error parsing args: %s", err)
	}

	// Check the flags are set correctly.
	for _, v := range []struct {
		loaded   interface{}
		expected interface{}
	}{
		{config.ListenNetwork, "unix"},
		{config.ListenAddress, "/var/run/dikastes/dikastes.sock"},
		{config.DialNetwork, "unix"},
		{config.DialAddress, ""},
		{config.LogLevel, "info"},
		{config.PerHostWAFEnabled, false},
		{config.WAFDirectives.Value(), []string{}},
	} {
		if fmt.Sprint(v.loaded) != fmt.Sprint(v.expected) {
			t.Errorf("Loaded flag is %v, but we expected %v", v.loaded, v.expected)
		}
	}
}

// TestAcceptLegacyArgs tests that the flags' can still accept legacy arguments.
func TestAcceptLegacyArgs(t *testing.T) {
	args := []string{"dikastes", "server", "-dial=/var/run/nodeagent/nodeagent.sock", "-listen=/var/run/dikastes/dikastes.sock"}

	config := flags.New()
	if err := config.Parse(args); err != nil {
		t.Errorf("error parsing args: %s", err)
	}

	// Check the flags are set correctly.
	for _, v := range []struct {
		loaded   interface{}
		expected interface{}
	}{
		{config.ListenNetwork, "unix"},
		{config.ListenAddress, "/var/run/dikastes/dikastes.sock"},
		{config.DialNetwork, "unix"},
		{config.DialAddress, "/var/run/nodeagent/nodeagent.sock"},
		{config.LogLevel, "info"},
		{config.PerHostWAFEnabled, false},
		{config.WAFDirectives.Value(), []string{}},
	} {
		if fmt.Sprint(v.loaded) != fmt.Sprint(v.expected) {
			t.Errorf("Loaded flag is %v, but we expected %v", v.loaded, v.expected)
		}
	}
}

func TestStringArrayArgs(t *testing.T) {
	args := []string{
		"dikastes", "server",
		// short flag
		"-waf-ruleset-file", "/etc/modsecurity-ruleset/tigera.conf",
		"-waf-directive", "Include @embedded/crs-setup.conf",
		// short flag, double quoted
		"-waf-directive", "SecRuleEngine Off",
		// short flag, eq-delimited, single quoted
		"-waf-directive='SecRuleEngine DetectionOnly'",
		// short flag, eq-delimited, double quoted
		"-waf-directive=\"SecRuleEngine On\"",
		// long flag
		"--waf-directive", "SecAuditLog Off",
		// long flag, eq-delimited, single quoted
		"--waf-directive='SecAuditLog /var/log/apache2/audit.log'",
		// long flag, eq-delimited, double quoted
		"--waf-directive=\"SecAuditLog /var/log/apache2/audit.log\"",
	}

	config := flags.New()
	if err := config.Parse(args); err != nil {
		t.Errorf("error parsing args: %s", err)
	}

	// Check the flags are set correctly.
	for _, v := range []struct {
		loaded   interface{}
		expected interface{}
	}{
		{
			config.WAFDirectives.Value(),
			[]string{
				"Include @embedded/crs-setup.conf",
				"SecRuleEngine Off",
				"SecRuleEngine DetectionOnly",
				"SecRuleEngine On",
				"SecAuditLog Off",
				"SecAuditLog /var/log/apache2/audit.log",
				"SecAuditLog /var/log/apache2/audit.log",
			},
		},
		{
			config.WAFRulesetFiles.Value(),
			[]string{"/etc/modsecurity-ruleset/tigera.conf"},
		},
	} {
		if fmt.Sprint(v.loaded) != fmt.Sprint(v.expected) {
			t.Errorf("Loaded flag is %v, but we expected %v", v.loaded, v.expected)
		}
	}
}

func TestBoolArgs(t *testing.T) {
	args := []string{"dikastes", "server", "-per-host-waf-enabled"}
	config := flags.New()
	if err := config.Parse(args); err != nil {
		t.Errorf("error parsing args: %s", err)
	}

	// Check the flags are set correctly.
	for _, v := range []struct {
		loaded   interface{}
		expected interface{}
	}{
		{config.PerHostWAFEnabled, true},
	} {
		if fmt.Sprint(v.loaded) != fmt.Sprint(v.expected) {
			t.Errorf("Loaded flag is %v, but we expected %v", v.loaded, v.expected)
		}
	}
}

func TestAcceptableArgs(t *testing.T) {
	for _, testCase := range []struct {
		args        []string
		expectedErr error
	}{
		{
			[]string{"dikastes", "server", "-dial=/var/run/nodeagent/nodeagent.sock", "-listen=/var/run/dikastes/dikastes.sock"}, nil,
		},
		{
			[]string{"dikastes", "server", "-dial", "/var/run/nodeagent/nodeagent.sock", "-listen", "/var/run/dikastes/dikastes.sock"}, nil,
		},
		{
			[]string{"dikastes", "-dial", "/var/run/nodeagent/nodeagent.sock", "-listen", "/var/run/dikastes/dikastes.sock"}, nil,
		},
		{
			[]string{"dikastes", "server"}, nil,
		},
		{
			[]string{"dikastes"}, nil,
		},
	} {
		config := flags.New()
		err := config.Parse(testCase.args)
		if err != testCase.expectedErr {
			t.Errorf("Expected error %v, but got %v", testCase.expectedErr, err)
		}
	}
}
