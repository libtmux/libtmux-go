//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go"
	"github.com/libtmux/libtmux-go/tmuxtest"
)

//libtmux:real-tmux
func TestShowBufferBytesPreservesRealTmuxData(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	want := []byte{0xff, 'A', '\n', '\n'}
	path := filepath.Join(t.TempDir(), "buffer.bin")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	name := "go-byte-buffer"
	if err := server.LoadBuffer(ctx, tmux.LoadBufferRequest{
		Path: path,
		Name: &name,
	}); err != nil {
		t.Fatalf("LoadBuffer() error = %v", err)
	}

	got, err := server.ShowBufferBytes(ctx, &name)
	if err != nil {
		t.Fatalf("ShowBufferBytes() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ShowBufferBytes() = %q, want %q", got, want)
	}
	got[0] = 'X'
	again, err := server.ShowBufferBytes(ctx, &name)
	if err != nil {
		t.Fatalf("second ShowBufferBytes() error = %v", err)
	}
	if !bytes.Equal(again, want) {
		t.Fatalf("second ShowBufferBytes() = %q, want %q", again, want)
	}
}

// libtmux:parity libtmux.server.Server.delete_buffer
// libtmux:parity libtmux.server.Server.delete_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.list_buffers
// libtmux:parity libtmux.server.Server.list_buffers#parameter-branch:filter:dad5b2f428ff
// libtmux:parity libtmux.server.Server.list_buffers#parameter-branch:format_string:7284ef554e76
// libtmux:parity libtmux.server.Server.load_buffer
// libtmux:parity libtmux.server.Server.load_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.save_buffer
// libtmux:parity libtmux.server.Server.save_buffer#parameter-branch:append:03665a1a84bd
// libtmux:parity libtmux.server.Server.save_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.set_buffer
// libtmux:parity libtmux.server.Server.set_buffer#parameter-branch:append:03665a1a84bd
// libtmux:parity libtmux.server.Server.set_buffer#parameter-branch:buffer_name:5c7057988ea3
// libtmux:parity libtmux.server.Server.show_buffer
// libtmux:parity libtmux.server.Server.show_buffer#parameter-branch:buffer_name:5c7057988ea3
//
//libtmux:real-tmux
func TestServerBuffersAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	name := "phase6-buffer"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{
		Data: "one\ntwo", Name: &name,
	}); err != nil {
		t.Fatalf("SetBuffer() error = %v", err)
	}
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{
		Data: "\nthree", Name: &name, Append: true,
	}); err != nil {
		t.Fatalf("SetBuffer(append) error = %v", err)
	}
	got, err := server.ShowBuffer(ctx, &name)
	if err != nil || got != "one\ntwo\nthree" {
		t.Fatalf("ShowBuffer() = (%q, %v), want multiline value", got, err)
	}

	format := "#{buffer_name}"
	filter := tmux.TmuxFilter("#{m:phase6-*,#{buffer_name}}")
	listed, err := server.ListBuffers(ctx, tmux.ListBuffersRequest{
		Format: &format, Filter: &filter,
	})
	if err != nil || !slices.Contains(listed, name) {
		t.Fatalf("ListBuffers() = (%#v, %v), want %q", listed, err, name)
	}
	malformed := tmux.TmuxFilter("#{")
	listed, err = server.ListBuffers(ctx, tmux.ListBuffersRequest{Filter: &malformed})
	if err != nil || listed == nil || len(listed) != 0 {
		t.Fatalf("ListBuffers(malformed filter) = (%#v, %v), want nonnil empty", listed, err)
	}

	directory := t.TempDir()
	saved := filepath.Join(directory, "saved")
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: saved, Name: &name}); err != nil {
		t.Fatalf("SaveBuffer() error = %v", err)
	}
	contents, err := os.ReadFile(saved)
	if err != nil || string(contents) != "one\ntwo\nthree" {
		t.Fatalf("saved contents = (%q, %v)", contents, err)
	}
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{Data: "tail", Name: &name}); err != nil {
		t.Fatalf("SetBuffer(save append) error = %v", err)
	}
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{
		Path: saved, Name: &name, Append: true,
	}); err != nil {
		t.Fatalf("SaveBuffer(append) error = %v", err)
	}
	contents, err = os.ReadFile(saved)
	if err != nil || string(contents) != "one\ntwo\nthreetail" {
		t.Fatalf("appended contents = (%q, %v)", contents, err)
	}
	if err := os.WriteFile(saved, []byte("loaded\nvalue"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := "phase6-loaded"
	if err := server.LoadBuffer(ctx, tmux.LoadBufferRequest{Path: saved, Name: &loaded}); err != nil {
		t.Fatalf("LoadBuffer() error = %v", err)
	}
	if got, err := server.ShowBuffer(ctx, &loaded); err != nil || got != "loaded\nvalue" {
		t.Fatalf("ShowBuffer(loaded) = (%q, %v)", got, err)
	}
	dash := "phase6-dash"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{Data: "-literal", Name: &dash}); err != nil {
		t.Fatalf("SetBuffer(dash data) error = %v", err)
	}
	if got, err := server.ShowBuffer(ctx, &dash); err != nil || got != "-literal" {
		t.Fatalf("ShowBuffer(dash data) = (%q, %v)", got, err)
	}

	if err := server.DeleteBuffer(ctx, &name); err != nil {
		t.Fatalf("DeleteBuffer() error = %v", err)
	}
	if _, err := server.ShowBuffer(ctx, &name); err == nil {
		t.Fatal("ShowBuffer(deleted) error = nil")
	}
}

//libtmux:real-tmux
func TestServerBuffersPreserveTerminalSemicolonsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	name := "phase6-semicolon;"
	data := `value\;`
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{Data: data, Name: &name}); err != nil {
		t.Fatalf("SetBuffer() error = %v", err)
	}
	if got, err := server.ShowBuffer(ctx, &name); err != nil || got != data {
		t.Fatalf("ShowBuffer() = (%q, %v), want %q", got, err, data)
	}

	format := "#{buffer_name};"
	listed, err := server.ListBuffers(ctx, tmux.ListBuffersRequest{Format: &format})
	if err != nil || !slices.Contains(listed, name+";") {
		t.Fatalf("ListBuffers(terminal semicolon format) = (%#v, %v)", listed, err)
	}

	path := filepath.Join(t.TempDir(), "saved;")
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: path, Name: &name}); err != nil {
		t.Fatalf("SaveBuffer() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != data {
		t.Fatalf("saved contents = (%q, %v), want %q", contents, err, data)
	}

	loaded := "phase6-loaded-semicolon;"
	if err := server.LoadBuffer(ctx, tmux.LoadBufferRequest{Path: path, Name: &loaded}); err != nil {
		t.Fatalf("LoadBuffer() error = %v", err)
	}
	if got, err := server.ShowBuffer(ctx, &loaded); err != nil || got != data {
		t.Fatalf("ShowBuffer(loaded) = (%q, %v), want %q", got, err, data)
	}
}

//libtmux:real-tmux
func TestServerBufferPathsPreserveLexicalSymlinkComponentsAgainstRealTmux(t *testing.T) {
	server := tmuxtest.NewServer(context.Background(), t).WithStrictErrors()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	nested := filepath.Join(target, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(nested, link); err != nil {
		t.Fatal(err)
	}

	name := "phase6-lexical"
	if err := server.SetBuffer(ctx, tmux.SetBufferRequest{Data: "saved", Name: &name}); err != nil {
		t.Fatal(err)
	}
	lexicalSaved := link + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "saved"
	if err := server.SaveBuffer(ctx, tmux.SaveBufferRequest{Path: lexicalSaved, Name: &name}); err != nil {
		t.Fatalf("SaveBuffer() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(target, "saved"))
	if err != nil || string(contents) != "saved" {
		t.Fatalf("symlink-resolved saved contents = (%q, %v)", contents, err)
	}
	if _, err := os.Stat(filepath.Join(root, "saved")); !os.IsNotExist(err) {
		t.Fatalf("lexically cleaned path unexpectedly exists: %v", err)
	}

	normalizedSaved := filepath.Join(root, "normalized-saved")
	harmlessComponents := normalizedSaved + string(os.PathSeparator) +
		"." + string(os.PathSeparator)
	if err := server.SaveBuffer(
		ctx,
		tmux.SaveBufferRequest{Path: harmlessComponents, Name: &name},
	); err != nil {
		t.Fatalf("SaveBuffer(harmless lexical components) error = %v", err)
	}
	contents, err = os.ReadFile(normalizedSaved)
	if err != nil || string(contents) != "saved" {
		t.Fatalf("normalized saved contents = (%q, %v)", contents, err)
	}

	if err := os.WriteFile(filepath.Join(target, "loaded"), []byte("loaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	lexicalLoaded := link + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "loaded"
	if err := server.LoadBuffer(ctx, tmux.LoadBufferRequest{Path: lexicalLoaded, Name: &name}); err != nil {
		t.Fatalf("LoadBuffer() error = %v", err)
	}
	if got, err := server.ShowBuffer(ctx, &name); err != nil || got != "loaded" {
		t.Fatalf("ShowBuffer() = (%q, %v), want loaded", got, err)
	}
}
