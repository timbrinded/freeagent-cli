package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validProfile() Profile {
	return Profile{
		Environment: EnvironmentSandbox,
		ClientID:    "client-id",
		RedirectURI: "http://127.0.0.1:8484/oauth/callback",
		APIVersion:  "2026-08-16",
	}
}

func TestRepositoryRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	repository := Repository{Path: path}
	want := New()
	want.DefaultProfile = "work"
	want.Profiles["work"] = validProfile()
	if err := repository.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repository.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DefaultProfile != want.DefaultProfile || got.Profiles["work"] != want.Profiles["work"] {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("configuration permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestRepositorySaveReplacesExistingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	repository := Repository{Path: path}
	first := New()
	first.DefaultProfile = "first"
	first.Profiles["first"] = validProfile()
	if err := repository.Save(first); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second := New()
	second.DefaultProfile = "second"
	profile := validProfile()
	profile.ReadOnly = true
	second.Profiles["second"] = profile
	if err := repository.Save(second); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := repository.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultProfile != "second" || !got.Profiles["second"].ReadOnly || len(got.Profiles) != 1 {
		t.Fatalf("replacement configuration = %#v", got)
	}
}

func TestRepositoryRejectsSecretFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"version":1,"profiles":{"work":{"environment":"sandbox","client_id":"id","client_secret":"must-not-be-here","redirect_uri":"http://127.0.0.1:8484/callback","api_version":"2026-08-16"}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (Repository{Path: path}).Load()
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown secret field error, got %v", err)
	}
}

func TestRepositoryRejectsTrailingJSON(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"version":1,"profiles":{}} {"version":1,"profiles":{}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Repository{Path: path}).Load(); err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestProfileOriginsAreFixed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		environment Environment
		want        string
	}{
		{EnvironmentSandbox, sandboxBaseURL},
		{EnvironmentProduction, productionBaseURL},
	}
	for _, test := range tests {
		profile := validProfile()
		profile.Environment = test.environment
		got, err := profile.BaseURL()
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != test.want {
			t.Fatalf("BaseURL() = %q, want %q", got, test.want)
		}
	}
}

func TestProfileValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Profile)
	}{
		{"unknown environment", func(p *Profile) { p.Environment = "custom" }},
		{"missing client", func(p *Profile) { p.ClientID = "" }},
		{"insecure remote redirect", func(p *Profile) { p.RedirectURI = "http://example.com/callback" }},
		{"localhost alias redirect", func(p *Profile) { p.RedirectURI = "http://localhost:8484/callback" }},
		{"redirect query", func(p *Profile) { p.RedirectURI += "?x=1" }},
		{"moving API version", func(p *Profile) { p.APIVersion = "latest" }},
		{"invalid API date", func(p *Profile) { p.APIVersion = "2026-99-99" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile := validProfile()
			test.mutate(&profile)
			if err := profile.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateProfileNameRejectsPathSegments(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "../escape", "a/b", `a\\b`, ".", "UPPER"} {
		if err := ValidateProfileName(name); err == nil {
			t.Errorf("ValidateProfileName(%q) accepted an unsafe name", name)
		}
	}
	for _, name := range []string{"sandbox", "client-1", "practice.main", "read_only"} {
		if err := ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q): %v", name, err)
		}
	}
}
