//go:build linux

package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type packageExit int

func (e packageExit) Error() string { return fmt.Sprintf("exit %d", e) }
func (e packageExit) ExitCode() int { return int(e) }

type packageFixture struct {
	t                         *testing.T
	location                  Location
	stage                     string
	oldBody, newBody          []byte
	installed, status, owner  string
	metadata                  map[string]string
	privileged                [][]string
	failInstall, failRollback int
	partial                   bool
	native                    bool
	client                    *Client
	next, previous            *Result
}

func newPackageFixture(t *testing.T, native bool) *packageFixture {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "安装目录 O'Brien [copy]*?", "koto-desktop")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	f := &packageFixture{t: t, location: Location{Executable: executable, Target: executable, Product: App}, stage: filepath.Join(dir, "stage"), installed: "0.1.0~beta.2", status: "install ok installed", owner: "koto", native: native, metadata: map[string]string{}}
	if err := os.Mkdir(f.stage, 0700); err != nil {
		t.Fatal(err)
	}
	oldExe := []byte("#!/bin/sh\nprintf 'koto 0.1.0-beta.2\\n'\n")
	newExe := []byte("#!/bin/sh\nprintf 'koto 0.1.0\\n'\n")
	if err := os.WriteFile(executable, oldExe, 0755); err != nil {
		t.Fatal(err)
	}
	f.oldBody, f.newBody = oldExe, newExe
	if native {
		f.oldBody = f.buildArchive(dir, "old", f.installed, oldExe)
		f.newBody = f.buildArchive(dir, "new", "0.1.0", newExe)
	}
	name := "koto-linux-" + runtime.GOARCH + ".deb"
	oldRelease, bodies := testRelease("v0.1.0-beta.2", name, f.oldBody)
	newRelease, newBodies := testRelease("v0.1.0", name, f.newBody)
	for k, v := range newBodies {
		bodies[k] = v
	}
	f.client = testClient([]release{newRelease, oldRelease}, bodies)
	f.previous = &Result{Product: App, Current: "0.1.0-beta.2", Latest: "0.1.0-beta.2", Tag: oldRelease.Tag, Asset: oldRelease.Assets[0], Checksums: oldRelease.Assets[1]}
	f.next = &Result{Product: App, Current: "0.1.0-beta.2", Latest: "0.1.0", Tag: newRelease.Tag, Available: true, Asset: newRelease.Assets[0], Checksums: newRelease.Assets[1]}
	return f
}
func (f *packageFixture) buildArchive(dir, name, version string, executable []byte) []byte {
	f.t.Helper()
	root := filepath.Join(dir, name)
	target := filepath.Join(root, strings.TrimPrefix(f.location.Executable, "/"))
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "DEBIAN"), 0755); err != nil {
		f.t.Fatal(err)
	}
	control := fmt.Sprintf("Package: koto\nVersion: %s\nArchitecture: %s\nMaintainer: Test <test@example.invalid>\nDescription: Isolated package update fixture\n", version, runtime.GOARCH)
	if err := os.WriteFile(filepath.Join(root, "DEBIAN/control"), []byte(control), 0644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(target, executable, 0755); err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(dir, name+".deb")
	if out, err := exec.Command("/usr/bin/dpkg-deb", "--root-owner-group", "--build", root, path).CombinedOutput(); err != nil {
		f.t.Fatalf("build DEB: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return data
}
func (f *packageFixture) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case "/usr/bin/dpkg-query":
		if reflect.DeepEqual(args, []string{"--admindir=/var/lib/dpkg", "--search", "--", escapeDpkgPattern(f.location.Executable)}) {
			if f.owner == "" {
				return nil, packageExit(1)
			}
			return []byte(f.owner + ": " + f.location.Executable + "\n"), nil
		}
		if !reflect.DeepEqual(args, []string{"--admindir=/var/lib/dpkg", "--show", "--showformat=${Package}\t${Version}\t${Architecture}\t${Status}\n", "koto"}) {
			f.t.Fatalf("unexpected query: %q", args)
		}
		return []byte("koto\t" + f.installed + "\t" + runtime.GOARCH + "\t" + f.status + "\n"), nil
	case "/usr/bin/dpkg-deb":
		if f.native {
			return runPackageCommand(ctx, name, args...)
		}
		body, err := os.ReadFile(args[1])
		if err != nil {
			return nil, err
		}
		version := "0.1.0"
		if string(body) == string(f.oldBody) {
			version = "0.1.0~beta.2"
		}
		if args[0] == "--field" {
			if out, ok := f.metadata[version]; ok {
				return []byte(out), nil
			}
			return []byte(fmt.Sprintf("Package: koto\nVersion: %s\nArchitecture: %s\n", version, runtime.GOARCH)), nil
		}
		if args[0] == "--extract" {
			target := filepath.Join(args[2], strings.TrimPrefix(f.location.Executable, "/"))
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(target, body, 0755)
		}
	case "/usr/bin/pkexec":
		original := append([]string(nil), args...)
		if len(args) == 8 && args[4] == "--refuse-downgrade" {
			args = append(append([]string(nil), args[:4]...), args[5:]...)
		}
		if len(args) != 7 || !reflect.DeepEqual(args[:6], []string{"--disable-internal-agent", "/usr/bin/dpkg", "--admindir=/var/lib/dpkg", "--root=/", "--install", "--"}) {
			f.t.Fatalf("unsafe privileged argv: %q", args)
		}
		f.privileged = append(f.privileged, append([]string(nil), args...))
		body, err := os.ReadFile(args[6])
		if err != nil {
			return nil, err
		}
		rollback := string(body) == string(f.oldBody)
		if !rollback && (len(original) != 8 || original[4] != "--refuse-downgrade") {
			f.t.Fatal("installation permits downgrading a concurrently changed package")
		}
		code := f.failInstall
		if rollback {
			code = f.failRollback
		}
		if code == 0 || (!rollback && f.partial) {
			f.installed = "0.1.0"
			f.status = "install ok installed"
			exe := []byte("#!/bin/sh\nprintf 'koto 0.1.0\\n'\n")
			if rollback {
				f.installed = "0.1.0~beta.2"
				exe = []byte("#!/bin/sh\nprintf 'koto 0.1.0-beta.2\\n'\n")
			}
			if f.partial && !rollback {
				f.status = "install ok unpacked"
			}
			if err := os.WriteFile(f.location.Executable, exe, 0755); err != nil {
				return nil, err
			}
		}
		if code != 0 {
			return []byte("package test failure"), packageExit(code)
		}
		return nil, nil
	default:
		if filepath.IsAbs(name) && strings.HasPrefix(name, f.stage+"/") && reflect.DeepEqual(args, []string{"--koto-update-version"}) {
			return runPackageCommand(ctx, name, args...)
		}
	}
	f.t.Fatalf("unexpected command: %s %q", name, args)
	return nil, errors.New("unexpected command")
}
func (f *packageFixture) prepare() *PackagePrepared {
	f.t.Helper()
	p, err := f.client.preparePackage(context.Background(), f.next, f.previous, f.location, f.stage, nil, f.run)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}
func (f *packageFixture) assertOld() {
	f.t.Helper()
	if f.installed != "0.1.0~beta.2" || f.status != "install ok installed" {
		f.t.Fatalf("package state changed: %s %s", f.installed, f.status)
	}
	body, err := os.ReadFile(f.location.Executable)
	if err != nil || string(body) != "#!/bin/sh\nprintf 'koto 0.1.0-beta.2\\n'\n" {
		f.t.Fatalf("old executable changed: %q %v", body, err)
	}
}
func TestDetectPackagePreciseOwnership(t *testing.T) {
	for _, tc := range []struct {
		owner, status    string
		wantNil, wantErr bool
	}{
		{"koto", "install ok installed", false, false},
		{"koto:" + runtime.GOARCH, "install ok installed", false, false},
		{"", "install ok installed", true, false},
		{"other", "install ok installed", false, true},
		{"koto, other", "install ok installed", false, true},
		{"koto", "hold ok installed", false, true},
		{"koto", "install ok half-configured", false, true},
	} {
		t.Run(tc.owner+tc.status, func(t *testing.T) {
			f := newPackageFixture(t, false)
			f.owner, f.status = tc.owner, tc.status
			p, err := detectPackage(context.Background(), f.location, f.run)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ownership error: %v", err)
			}
			if !tc.wantErr && (p == nil) != tc.wantNil {
				t.Fatalf("ownership: %+v", p)
			}
		})
	}
}
func TestPreparePackageBothVerifiedAndTrial(t *testing.T) {
	f := newPackageFixture(t, false)
	p := f.prepare()
	if p.TrialExecutable == "" || p.BeforeSHA256 == "" || p.NewSHA256 == p.OldSHA256 {
		t.Fatal("missing verified package state")
	}
	if _, err := os.Stat(filepath.Join(f.stage, "previous")); !os.IsNotExist(err) {
		t.Fatal("old extracted files retained")
	}
	if len(f.privileged) != 0 {
		t.Fatal("prepare requested privileges")
	}
	f.assertOld()
	// Recovery state survives JSON without persisting an executor or process.
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var restored PackagePrepared
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restored.run = f.run
	if err = restored.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestPreparePackageRejectsMissingRollbackAndMetadata(t *testing.T) {
	for _, kind := range []string{"missing-old", "wrong-old-version", "wrong-name", "wrong-architecture", "locally-modified", "untrusted-stage", "wrong-trial"} {
		t.Run(kind, func(t *testing.T) {
			f := newPackageFixture(t, false)
			switch kind {
			case "missing-old":
				f.previous.Asset.URL += "-missing"
			case "wrong-old-version":
				f.previous.Latest = "0.0.1"
			case "wrong-name":
				f.metadata["0.1.0"] = "Package: unrelated\nVersion: 0.1.0\nArchitecture: " + runtime.GOARCH + "\n"
			case "wrong-architecture":
				f.metadata["0.1.0~beta.2"] = "Package: koto\nVersion: 0.1.0~beta.2\nArchitecture: all\n"
			case "locally-modified":
				if err := os.WriteFile(f.location.Executable, []byte("local change"), 0755); err != nil {
					t.Fatal(err)
				}
			case "untrusted-stage":
				if err := os.Chmod(f.stage, 0755); err != nil {
					t.Fatal(err)
				}
			}
			run := f.run
			if kind == "wrong-trial" {
				run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
					if strings.HasPrefix(name, f.stage+"/") {
						return []byte("koto 9.0.0"), nil
					}
					return f.run(ctx, name, args...)
				}
			}
			if _, err := f.client.preparePackage(context.Background(), f.next, f.previous, f.location, f.stage, nil, run); err == nil {
				t.Fatal("bad update accepted")
			}
			if len(f.privileged) != 0 {
				t.Fatal("invalid preparation requested privilege")
			}
			entries, _ := os.ReadDir(f.stage)
			if len(entries) != 0 {
				t.Fatalf("failed preparation left files: %v", entries)
			}
		})
	}
}
func TestPackageInstallAuthorizationAndRollback(t *testing.T) {
	for _, kind := range []string{"success", "cancelled", "denied", "partial", "rollback-cancelled", "independent-change", "modified-archive", "modified-install"} {
		t.Run(kind, func(t *testing.T) {
			f := newPackageFixture(t, false)
			p := f.prepare()
			switch kind {
			case "cancelled":
				f.failInstall = 126
			case "denied":
				f.failInstall = 127
			case "partial":
				f.failInstall = 1
				f.partial = true
			case "modified-archive":
				if err := os.WriteFile(p.NewDEB, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			case "modified-install":
				if err := os.WriteFile(f.location.Executable, []byte("changed"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			err := p.Install(context.Background())
			wantErr := kind == "cancelled" || kind == "denied" || kind == "partial" || kind == "modified-archive" || kind == "modified-install"
			if (err != nil) != wantErr {
				t.Fatalf("install error: %v", err)
			}
			if kind == "modified-archive" || kind == "modified-install" {
				if len(f.privileged) != 0 || p.NeedsRecovery {
					t.Fatal("changed input reached installer")
				}
				return
			}
			for _, file := range []string{p.NewDEB, p.OldDEB} {
				if _, e := os.Stat(file); e != nil {
					t.Fatal("recovery archive removed")
				}
			}
			if kind == "cancelled" || kind == "denied" {
				f.assertOld()
				if p.NeedsRecovery {
					t.Fatal("cancelled update needs recovery")
				}
				if e := p.Rollback(context.Background()); e != nil {
					t.Fatal(e)
				}
				if len(f.privileged) != 1 {
					t.Fatal("cancelled update re-requested authorization")
				}
				return
			}
			if !p.NeedsRecovery {
				t.Fatal("replacement not protected by recovery state")
			}
			if kind == "rollback-cancelled" {
				f.failRollback = 126
			}
			if kind == "independent-change" {
				f.installed = "0.2.0"
			}
			err = p.Rollback(context.Background())
			if kind == "rollback-cancelled" || kind == "independent-change" {
				if err == nil || !p.NeedsRecovery {
					t.Fatal("failed recovery silently accepted")
				}
				if _, e := os.Stat(p.OldDEB); e != nil {
					t.Fatal("failed rollback removed archive")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.assertOld()
			if p.NeedsRecovery {
				t.Fatal("successful rollback still marked pending")
			}
			if len(f.privileged) != 2 || f.privileged[1][6] != p.OldDEB {
				t.Fatal("rollback did not use old package through dpkg")
			}
		})
	}
}
func TestPackageInstallCancelledContextDoesNotStart(t *testing.T) {
	f := newPackageFixture(t, false)
	p := f.prepare()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Install(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error: %v", err)
	}
	if len(f.privileged) != 0 {
		t.Fatal("cancelled operation requested privileges")
	}
	f.assertOld()
}
func TestNativeDEBMetadataExtractionAndTrial(t *testing.T) {
	if _, err := os.Stat("/usr/bin/dpkg-deb"); err != nil {
		t.Skip("dpkg-deb unavailable on this distribution")
	}
	f := newPackageFixture(t, true)
	p := f.prepare()
	if err := p.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertOld()
}
