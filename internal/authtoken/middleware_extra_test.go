package authtoken

import (
	"os"
	"testing"
)

func TestSecretFromEnvSet(t *testing.T) {
	old, had := os.LookupEnv("SERVICE_TOKEN_SECRET")
	oldDM, hadDM := os.LookupEnv("DEV_MODE")
	os.Setenv("SERVICE_TOKEN_SECRET", "real-secret")
	os.Unsetenv("DEV_MODE")
	defer func() {
		if had {
			os.Setenv("SERVICE_TOKEN_SECRET", old)
		} else {
			os.Unsetenv("SERVICE_TOKEN_SECRET")
		}
		if hadDM {
			os.Setenv("DEV_MODE", oldDM)
		} else {
			os.Unsetenv("DEV_MODE")
		}
	}()
	s, bypass := SecretFromEnv()
	if s != "real-secret" || bypass {
		t.Fatalf("SecretFromEnv = (%q, %v), want (real-secret, false)", s, bypass)
	}
}

func TestSecretFromEnvDevMode(t *testing.T) {
	old, had := os.LookupEnv("SERVICE_TOKEN_SECRET")
	oldDM, hadDM := os.LookupEnv("DEV_MODE")
	os.Unsetenv("SERVICE_TOKEN_SECRET")
	os.Setenv("DEV_MODE", "1")
	defer func() {
		if had {
			os.Setenv("SERVICE_TOKEN_SECRET", old)
		} else {
			os.Unsetenv("SERVICE_TOKEN_SECRET")
		}
		if hadDM {
			os.Setenv("DEV_MODE", oldDM)
		} else {
			os.Unsetenv("DEV_MODE")
		}
	}()
	s, bypass := SecretFromEnv()
	if s != "" || !bypass {
		t.Fatalf("SecretFromEnv dev = (%q, %v), want (\", true)", s, bypass)
	}
}