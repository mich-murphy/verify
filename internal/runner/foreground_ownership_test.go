package runner

import (
	"strings"
	"testing"
)

func TestForegroundOwnerEnvironmentReserved(t *testing.T) {
	t.Setenv(ForegroundOwnerEnv, "standalone")
	for _, value := range []string{"", "0", "standalone", "runner", "1"} {
		t.Run(value, func(t *testing.T) {
			config := Config{Inherit: []string{ForegroundOwnerEnv}, Env: map[string]string{ForegroundOwnerEnv: "0"}}
			extra := map[string]string{ForegroundOwnerEnv: value}
			env, err := environment(config, extra)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, entry := range env {
				if strings.HasPrefix(entry, ForegroundOwnerEnv+"=") {
					count++
					if entry != ForegroundOwnerEnv+"=1" {
						t.Fatalf("unreserved ownership: %q", entry)
					}
				}
			}
			if count != 1 || extra[ForegroundOwnerEnv] != value || config.Env[ForegroundOwnerEnv] != "0" {
				t.Fatal("marker missing/duplicated or caller environment mutated")
			}
		})
	}
}

func TestForegroundOwnerEnvironmentInjectedWithoutInputs(t *testing.T) {
	env, err := environment(Config{}, nil)
	if err != nil || len(env) != 1 || env[0] != ForegroundOwnerEnv+"=1" {
		t.Fatalf("effective marker missing: %v, %v", env, err)
	}
}
