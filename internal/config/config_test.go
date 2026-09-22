package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeysPreferTheEnvironmentThenTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# keys\nexport TYPESAFE_API_KEY=\"from-file\"\nOPENROUTER_API_KEY='or-file'\nBROKEN LINE\n"), 0o600)
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "from-env")
	k, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := k.Get("TYPESAFE_API_KEY"); got != "from-file" {
		t.Errorf("TYPESAFE_API_KEY = %q", got)
	}
	if got := k.Get("OPENROUTER_API_KEY"); got != "from-env" {
		t.Errorf("OPENROUTER_API_KEY = %q", got)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatal(err)
	}
}
