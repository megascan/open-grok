package voice

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderArgs(t *testing.T) {
	arecord := Recorder{kind: "arecord"}
	args := arecord.Args(16000)
	if !contains(args, "S16_LE") || !contains(args, "raw") {
		t.Fatalf("%v", args)
	}
	c := indexOf(args, "-c")
	if c < 0 || args[c+1] != "1" {
		t.Fatalf("channels %v", args)
	}
	r := indexOf(args, "-r")
	if r < 0 || args[r+1] != "16000" {
		t.Fatalf("rate %v", args)
	}
	if args[len(args)-1] != "-" {
		t.Fatalf("stdout target %v", args)
	}

	parec := Recorder{kind: "parec"}.Args(24000)
	if !contains(parec, "--raw") || !contains(parec, "--format=s16le") || !contains(parec, "--rate=24000") || !contains(parec, "--channels=1") {
		t.Fatalf("%v", parec)
	}

	pw := Recorder{kind: "pw-record"}.Args(48000)
	if !contains(pw, "--raw") {
		t.Fatal("pw-record must use --raw")
	}
	ri := indexOf(pw, "--rate")
	if ri < 0 || pw[ri+1] != "48000" {
		t.Fatalf("rate %v", pw)
	}
	fi := indexOf(pw, "--format")
	if fi < 0 || pw[fi+1] != "s16" {
		t.Fatalf("format %v", pw)
	}
	ci := indexOf(pw, "--channels")
	if ci < 0 || pw[ci+1] != "1" {
		t.Fatalf("channels %v", pw)
	}
	if pw[len(pw)-1] != "-" {
		t.Fatalf("stdout %v", pw)
	}
}

func TestRecorderPreference(t *testing.T) {
	orig := probePATH
	t.Cleanup(func() { probePATH = orig })

	probePATH = func(string) bool { return true }
	rec, ok := DetectRecorder()
	if !ok || rec.Name() != "pw-record" {
		t.Fatalf("all present: %+v %v", rec, ok)
	}

	probePATH = func(name string) bool { return name != "pw-record" }
	rec, ok = DetectRecorder()
	if !ok || rec.Name() != "parec" {
		t.Fatalf("no pw-record: %+v %v", rec, ok)
	}

	probePATH = func(name string) bool { return name == "arecord" }
	rec, ok = DetectRecorder()
	if !ok || rec.Name() != "arecord" {
		t.Fatalf("alsa only: %+v %v", rec, ok)
	}

	probePATH = func(string) bool { return false }
	if _, ok := DetectRecorder(); ok {
		t.Fatal("none")
	}
}

func TestProbeInputDevice(t *testing.T) {
	orig := probePATH
	t.Cleanup(func() { probePATH = orig })
	probePATH = func(name string) bool { return name == "parec" }
	info, err := ProbeInputDevice()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "parec" || info.Detail != DeviceDetail {
		t.Fatalf("%+v", info)
	}
	if got := DoctorMicrophone(); got != "parec ("+DeviceDetail+")" {
		t.Fatalf("%q", got)
	}
}

func TestSpawnFakeRecorder(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pw-record")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec /bin/cat /dev/zero\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := probePATH
	t.Cleanup(func() { probePATH = orig })
	t.Setenv("PATH", dir)
	probePATH = executableOnPATH
	rec, ok := DetectRecorder()
	if !ok || rec.Name() != "pw-record" {
		t.Fatalf("%+v %v", rec, ok)
	}
	cmd, stdout, err := rec.Spawn(16000)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()
	if f, ok := stdout.(*os.File); ok {
		_ = f.SetReadDeadline(time.Now().Add(2 * time.Second))
	}
	buf := make([]byte, 64)
	n, err := stdout.Read(buf)
	if n == 0 {
		t.Fatalf("no pcm: %v", err)
	}
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}
