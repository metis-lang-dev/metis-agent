package dagent

// spec/17 §6 (linker s-5908724a step 4): the image a lone package links to (metisc --link PKGDIR, the one-member
// composition of itself), and what the Go loader reads from it: the member's bundle (members/<m>/: ground.wire,
// sidecar.json), its host binding and clock (image.json), tools.json at the image root, and the image's hash and
// lock for the handshake.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Image is a linked image as the loader keeps it.
type Image struct {
	Dir    string // IMAGE: <name>.image.v-<h12>
	Hash   string // the twelve hex digits of its name
	Lock   string // IMAGE/image.lock (the handshake's)
	Member ImageMember
}

// ImageMember is one member's entry of image.json.
type ImageMember struct {
	Name   string            `json:"name"`
	Bundle string            `json:"bundle"`
	Host   map[string]string `json:"host"` // {py, go, origin}; null: no host bound (load-host-missing when one is named)
	Clock  map[string]string `json:"clock"`
}

// ReadImage reads IMAGE/image.json for member NAME.
func ReadImage(dir, name string) (Image, error) {
	b, err := os.ReadFile(filepath.Join(dir, "image.json"))
	if err != nil {
		return Image{}, err
	}
	var doc struct {
		Format  string        `json:"format"`
		Members []ImageMember `json:"members"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Image{}, fmt.Errorf("%s/image.json: %w", dir, err)
	}
	if doc.Format != "metis-image/1" {
		return Image{}, fmt.Errorf("%s/image.json: format %q, want metis-image/1", dir, doc.Format)
	}
	img := Image{Dir: dir, Lock: filepath.Join(dir, "image.lock")}
	if i := strings.LastIndex(filepath.Base(dir), ".image.v-"); i >= 0 {
		img.Hash = filepath.Base(dir)[i+len(".image.v-"):]
	}
	for _, m := range doc.Members {
		if m.Name == name {
			img.Member = m
			return img, nil
		}
	}
	return Image{}, fmt.Errorf("%s/image.json: no member %s", dir, name)
}

// Steps is the member's clock steps (the watch steps one beat may take), 0 when the clock says none.
func (m ImageMember) Steps() int {
	n, _ := strconv.Atoi(m.Clock["steps"])
	return n
}

// CheckHost is the loader's host contract (spec/17 §6, ruling 4): a member whose manifest names a host but whose
// image binds none is refused (load-host-missing: the host's file was not beside the package at link); an image's
// go binding must be the registry's name for it (load-host-mismatch).
func (img Image) CheckHost(domain string, named bool, registry bool) error {
	if named && img.Member.Host == nil {
		return fmt.Errorf("load-host-missing: domain %s: its image (v-%s) binds no host: the host's file was not beside the package at link", domain, img.Hash)
	}
	if g := img.Member.Host["go"]; g != "" && (g != domain || !registry) {
		return fmt.Errorf("load-host-mismatch: domain %s: its image binds the go host %q, the registry has %q", domain, g, domain)
	}
	return nil
}
