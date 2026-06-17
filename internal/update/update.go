package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidManifest  = errors.New("invalid update manifest")
	ErrDigestMismatch   = errors.New("update digest mismatch")
	ErrInvalidSignature = errors.New("update signature invalid")
)

type Manifest struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

type VerifiedUpdate struct {
	Manifest Manifest
	Binary   []byte
}

type Client struct {
	HTTPClient *http.Client
	PublicKey  ed25519.PublicKey
}

func ParsePublicKey(encoded string) (ed25519.PublicKey, error) {
	decoded, err := decodeBytes(encoded)
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key length = %d, want %d", len(decoded), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(decoded), nil
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if strings.TrimSpace(manifest.Version) == "" || strings.TrimSpace(manifest.URL) == "" || strings.TrimSpace(manifest.SHA256) == "" || strings.TrimSpace(manifest.Signature) == "" {
		return Manifest{}, ErrInvalidManifest
	}
	return manifest, nil
}

func (c Client) FetchAndVerify(ctx context.Context, manifestURL string) (VerifiedUpdate, error) {
	if len(c.PublicKey) != ed25519.PublicKeySize {
		return VerifiedUpdate{}, fmt.Errorf("public key length = %d, want %d", len(c.PublicKey), ed25519.PublicKeySize)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	manifestBytes, err := fetch(ctx, client, manifestURL)
	if err != nil {
		return VerifiedUpdate{}, err
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return VerifiedUpdate{}, err
	}
	binary, err := fetch(ctx, client, manifest.URL)
	if err != nil {
		return VerifiedUpdate{}, err
	}
	if err := VerifyBinary(manifest, binary, c.PublicKey); err != nil {
		return VerifiedUpdate{}, err
	}
	return VerifiedUpdate{Manifest: manifest, Binary: binary}, nil
}

func VerifyBinary(manifest Manifest, binary []byte, publicKey ed25519.PublicKey) error {
	sum := sha256.Sum256(binary)
	expected, err := hex.DecodeString(strings.TrimSpace(manifest.SHA256))
	if err != nil {
		return err
	}
	if len(expected) != sha256.Size || !equalBytes(sum[:], expected) {
		return ErrDigestMismatch
	}
	signature, err := decodeBytes(manifest.Signature)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, binary, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func AtomicReplace(path string, binary []byte, mode os.FileMode) error {
	if path == "" {
		return errors.New("target path is required")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s returned status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func decodeBytes(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(encoded); err == nil {
		return decoded, nil
	}
	return hex.DecodeString(encoded)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := range a {
		result |= a[i] ^ b[i]
	}
	return result == 0
}
