package acceptance

import (
	"flag"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// exercised records every CLI command the acceptance tests run through the real
// binary. TestMain fails the package if the shipped help lists a command that
// no test ran, so adding a command without testing it cannot pass CI.
var exercised sync.Map

func recordCommand(arguments ...string) {
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "-") {
			exercised.Store(argument, true)
			return
		}
	}
}

var helpCommand = regexp.MustCompile(`(?m)^\s+workcopy ([a-z][a-z-]*)\b`)

// shippedCommands reads the command list from the binary's own help.
func shippedCommands() ([]string, error) {
	output, err := exec.Command(executable, "help").Output()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, match := range helpCommand.FindAllStringSubmatch(string(output), -1) {
		seen[match[1]] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// fullRun is false when the run was narrowed with -run, where coverage of the
// whole package is not expected.
func fullRun() bool {
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		return false
	}
	if f := flag.Lookup("test.short"); f != nil && f.Value.String() == "true" {
		return false
	}
	return true
}

func verifyCommandCoverage() error {
	commands, err := shippedCommands()
	if err != nil {
		return err
	}
	if len(commands) < 15 {
		return fmt.Errorf("could not read the command list from help (%v)", commands)
	}
	missing := []string{}
	for _, name := range commands {
		if _, ok := exercised.Load(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("CLI commands never run by an acceptance test: %s", strings.Join(missing, ", "))
	}
	return nil
}
