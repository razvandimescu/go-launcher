package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadVerifiesChecksum(t *testing.T) {
	content := []byte("child binary")
	src := filepath.Join(t.TempDir(), "child")
	if err := os.WriteFile(src, content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	tests := []struct {
		checksum string
		wantErr  bool
	}{
		{"", false},
		{"sha256:" + digest, false},
		{"sha256:" + strings.ToUpper(digest), false},
		{"sha256:" + strings.Repeat("0", 64), true},
		{"sha512:" + digest, true},
		{digest, true},
		{"sha256:", true},
	}
	for _, tt := range tests {
		cfg := &Config{DataDir: t.TempDir(), ChildBinaryName: "child", Fetcher: &fakeFetcher{binaryPath: src}}
		err := downloadToStaging(context.Background(), cfg, &Release{Version: "1.0.0", Checksum: tt.checksum})
		if (err != nil) != tt.wantErr {
			t.Errorf("checksum %q: err = %v, wantErr %v", tt.checksum, err, tt.wantErr)
		}
	}
}
