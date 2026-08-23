package voice

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// StartGrace is how long to wait after spawn before deciding the recorder died.
const StartGrace = 300 * time.Millisecond

// ReadChunk is the PCM stdout read size (~64 ms at 16 kHz mono PCM16).
const ReadChunk = 2048

// ErrNoRecorder is the Rust PATH-miss copy.
const ErrNoRecorder = "no microphone recorder found on PATH: install pipewire (pw-record), pulseaudio-utils (parec), or alsa-utils (arecord)"

// DeviceDetail is Rust input_device_info().detail.
const DeviceDetail = "system recorder; uses the audio server's default input"

// Recorder is a Linux system capture backend that emits raw PCM16 LE mono.
type Recorder struct {
	kind string
}

// Name is the executable (pw-record, parec, or arecord).
func (r Recorder) Name() string { return r.kind }

var recorderOrder = []string{"pw-record", "parec", "arecord"}

// probePATH reports whether name is an executable regular file on PATH.
// Tests replace this to avoid mutating the process PATH.
var probePATH = executableOnPATH

func executableOnPATH(name string) bool {
	path := os.Getenv("PATH")
	if path == "" {
		return false
	}
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, name)
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if st.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

// DetectRecorder returns the first recorder on PATH (PipeWire > PulseAudio > ALSA).
func DetectRecorder() (Recorder, bool) {
	for _, name := range recorderOrder {
		if probePATH(name) {
			return Recorder{kind: name}, true
		}
	}
	return Recorder{}, false
}

// InputDeviceInfo is the doctor / probe row for the recorder that would be spawned.
type InputDeviceInfo struct {
	Name   string
	Detail string
}

// ProbeInputDevice matches Rust input_device_info().
func ProbeInputDevice() (InputDeviceInfo, error) {
	rec, ok := DetectRecorder()
	if !ok {
		return InputDeviceInfo{}, fmt.Errorf("%s", ErrNoRecorder)
	}
	return InputDeviceInfo{Name: rec.Name(), Detail: DeviceDetail}, nil
}

// DoctorMicrophone formats the /doctor Voice microphone row.
func DoctorMicrophone() string {
	info, err := ProbeInputDevice()
	if err != nil {
		return "none detected"
	}
	return info.Name + " (" + info.Detail + ")"
}

// Args returns the argv that emits signed 16-bit little-endian mono PCM at rate Hz.
func (r Recorder) Args(rate uint32) []string {
	if rate == 0 {
		rate = DefaultSampleRate
	}
	rs := fmt.Sprintf("%d", rate)
	switch r.kind {
	case "pw-record":
		return []string{"--raw", "--rate", rs, "--channels", "1", "--format", "s16", "-"}
	case "parec":
		return []string{"--raw", "--format=s16le", "--rate=" + rs, "--channels=1"}
	case "arecord":
		return []string{"-q", "-t", "raw", "-f", "S16_LE", "-c", "1", "-r", rs, "-"}
	default:
		return nil
	}
}

// Spawn starts the recorder with stdout piped and confirms it survived StartGrace.
func (r Recorder) Spawn(rate uint32) (*exec.Cmd, io.ReadCloser, error) {
	cmd := exec.Command(r.kind, r.Args(rate)...)
	cmd.Stdin = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start %s: %w", r.kind, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start %s: %w", r.kind, err)
	}
	time.Sleep(StartGrace)
	// ProcessState is only set after Wait; use WNOHANG so a recorder that
	// died during the grace period is reported instead of a silent EOF.
	var status syscall.WaitStatus
	wpid, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil)
	if err == nil && wpid == cmd.Process.Pid {
		_ = stdout.Close()
		return nil, nil, fmt.Errorf("%s exited immediately (%v)", r.kind, status)
	}
	return cmd, stdout, nil
}

// FakeRecorder is a PATH-free recorder for tests.
func FakeRecorder(name string) Recorder { return Recorder{kind: name} }
