package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// root is the repository root, from this package's directory.
const root = ".."

// These files cannot be built or installed on a development machine — there is
// no Docker, no Helm and no systemd on the machine this was written on — so the
// checks are the shallow kind: does it parse, does the template compile, is the
// directive that matters present. That catches typos and omissions, and it does
// not pretend to be more than it is.

func TestYAMLFilesParse(t *testing.T) {
	for _, path := range []string{
		"docker-compose.yml",
		"configs/notifyrelay.example.yaml",
		"deploy/helm/notifyrelay/Chart.yaml",
		"deploy/helm/notifyrelay/values.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			var doc any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("does not parse: %v", err)
			}
			if doc == nil {
				t.Error("parsed to nothing")
			}
		})
	}
}

// A Helm template is a Go template with extra functions. Parsing it here does
// not check that the chart is correct — the sprig and Helm functions are not
// defined at parse time — but it does catch an unclosed action or a stray
// brace, which is the mistake that turns `helm install` into a stack trace.
func TestHelmTemplatesCompile(t *testing.T) {
	dir := filepath.Join(root, "deploy", "helm", "notifyrelay", "templates")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}

		// The Helm and sprig functions have to exist for the parse to get past
		// them: Go resolves function names while parsing, so an unregistered
		// `include` is reported as a parse error rather than at execution.
		// Stubs are enough — nothing here is executed.
		if _, err := template.New(entry.Name()).Funcs(helmStubs()).Parse(string(raw)); err != nil {
			t.Errorf("%s does not compile as a template: %v", entry.Name(), err)
		}
		checked++
	}

	if checked == 0 {
		t.Fatal("no templates were checked")
	}
}

// directivesOnly strips comment lines.
//
// Every check in this file that greps for a directive would otherwise match the
// comment explaining why that directive is written the way it is — a comment
// saying "do not write ${SMTP_PASSWORD:-}" contains ${SMTP_PASSWORD:-}. A check
// that cannot tell prose from a directive is a check that gets deleted the
// second time it cries wolf.
func directivesOnly(text, comment string) string {
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), comment) {
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

// helmStubs stands in for the functions Helm and sprig provide.
//
// The signatures do not matter: parsing only needs the names to resolve.
func helmStubs() template.FuncMap {
	stub := func(...any) string { return "" }
	names := []string{
		"include", "default", "trunc", "trimSuffix", "contains", "replace",
		"nindent", "indent", "toYaml", "toJson", "quote", "sha256sum", "printf",
		"empty", "not", "and", "or", "ternary", "required", "tpl", "lookup",
	}
	m := template.FuncMap{}
	for _, name := range names {
		m[name] = stub
	}
	return m
}

// The chart must not be installable in a shape that silently does not work.
func TestHelmValuesCarryTheSettingsThatMatter(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "helm", "notifyrelay", "values.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var values struct {
		ReplicaCount int `yaml:"replicaCount"`
		Config       struct {
			Storage struct {
				Path     string `yaml:"path"`
				SpoolDir string `yaml:"spool_dir"`
			} `yaml:"storage"`
		} `yaml:"config"`
		Persistence struct {
			Enabled    bool   `yaml:"enabled"`
			AccessMode string `yaml:"accessMode"`
		} `yaml:"persistence"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("parse: %v", err)
	}

	// One replica, because the queue is one SQLite file: a second pod would be
	// a second relay with its own queue rather than a copy of this one.
	if values.ReplicaCount != 1 {
		t.Errorf("replicaCount = %d, want 1 — the queue is a single SQLite file",
			values.ReplicaCount)
	}

	// ReadWriteOnce for the same reason: SQLite takes an exclusive lock, and a
	// network filesystem does not honour it.
	if values.Persistence.AccessMode != "ReadWriteOnce" {
		t.Errorf("accessMode = %q, want ReadWriteOnce", values.Persistence.AccessMode)
	}

	// The database has to be on the volume that gets backed up.
	if !strings.HasPrefix(values.Config.Storage.Path, "/data/") {
		t.Errorf("storage.path = %q, want it under /data", values.Config.Storage.Path)
	}
	if !strings.HasPrefix(values.Config.Storage.SpoolDir, "/data/") {
		t.Errorf("storage.spool_dir = %q, want it under /data", values.Config.Storage.SpoolDir)
	}
}

// The unit has to name the directory the service writes to, or it fails at
// startup on the first run — which is the failure this whole file exists to
// catch before somebody deploys it.
func TestSystemdUnitHasTheDirectivesThatMatter(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "systemd", "notifyrelay.service"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	unit := string(raw)

	for _, want := range []struct {
		directive string
		why       string
	}{
		{"WorkingDirectory=/var/lib/notifyrelay",
			"the database is created relative to the working directory on first run"},
		{"ReadWritePaths=/var/lib/notifyrelay",
			"ProtectSystem=strict makes everything else read-only"},
		{"ExecReload=/bin/kill -HUP $MAINPID",
			"systemctl reload must reach the SIGHUP handler"},
		{"User=notifyrelay", "the service must not run as root"},
		{"Restart=on-failure", "a crash-looping relay is better than a silent one"},
	} {
		if !strings.Contains(unit, want.directive) {
			t.Errorf("the unit has no %q — %s", want.directive, want.why)
		}
	}

	// A stop timeout shorter than a delivery would kill a message mid-flight.
	if !strings.Contains(unit, "TimeoutStopSec=") {
		t.Error("the unit sets no TimeoutStopSec; the default is 90s and nobody knows that")
	}
}

// The image has no shell, so a container healthcheck has to be the binary
// probing itself. If the Dockerfile declared one that needs curl, it would fail
// on every container and take the deployment down with it.
func TestDockerfileHealthcheckUsesTheBinary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	dockerfile := string(raw)

	if !strings.Contains(dockerfile, "HEALTHCHECK") {
		t.Fatal("the image declares no healthcheck")
	}
	if !strings.Contains(dockerfile, `"/notifyrelay", "--healthcheck"`) {
		t.Error("the healthcheck does not run the binary's own probe")
	}

	// Only the directive, not the comments around it. A comment explaining
	// that distroless has no curl mentions curl, and a check that cannot tell
	// prose from a directive is a check that gets deleted the second time it
	// cries wolf.
	var directive strings.Builder
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		directive.WriteString(line)
		directive.WriteString("\n")
	}

	for _, shellTool := range []string{"curl", "wget", "CMD-SHELL", "sh -c"} {
		if strings.Contains(directive.String(), shellTool) {
			t.Errorf("the image uses %q, which a distroless base does not have", shellTool)
		}
	}

	// distroless has no writable working directory by default, and the service
	// creates its database on first run.
	if !strings.Contains(dockerfile, "WORKDIR /app") {
		t.Error("no WORKDIR: the nonroot user cannot create the database in /")
	}
}

// The compose file must not define a secret variable as an empty string when
// nobody set it. The configuration loader treats "set but empty" as a real
// value, so a deployment that meant to have no password would start with one.
func TestComposePassesSecretsThroughRatherThanDefaulting(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	compose := directivesOnly(string(raw), "#")

	if strings.Contains(compose, "${SMTP_PASSWORD:-}") {
		t.Error("a secret is defaulted to empty; an unset variable must stay unset")
	}
	if !strings.Contains(compose, "- SMTP_PASSWORD") {
		t.Error("SMTP_PASSWORD is not passed through from the environment")
	}

	// The data volume is what gets backed up. Without it the queue lives in the
	// container's writable layer and disappears with the container.
	if !strings.Contains(compose, "notifyrelay-data:/app/data") {
		t.Error("the database is not on a named volume")
	}
}
