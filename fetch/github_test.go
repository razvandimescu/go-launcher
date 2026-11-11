package fetch

import "testing"

func TestMalformedAssetPatternIsAnError(t *testing.T) {
	f := &githubFetcher{assetPattern: "app-[*"}
	if a, err := f.matchAsset([]ghAsset{{Name: "app-[x"}}); err == nil {
		t.Errorf("malformed pattern matched %q", a.Name)
	}
}
