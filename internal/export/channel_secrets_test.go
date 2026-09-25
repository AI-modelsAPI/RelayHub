package export

import (
	"context"
	"strings"
	"testing"

	"relayhub/internal/domain"
)

func seedSecretChannel(t *testing.T) (ctx context.Context, repoCh domain.Channel) {
	t.Helper()
	return context.Background(), domain.Channel{
		ID: "c1", ProviderID: "p1", Name: "C1", BaseURL: "https://relay.example.com", Enabled: true,
		ProxyURL:      "socks5://alice:proxy-pass-123@127.0.0.1:1080",
		CustomHeaders: map[string]string{"Authorization": "Bearer hdr-secret-456", "User-Agent": "ua/1"},
	}
}

// AUDIT 2026-09-24 F10: proxy passwords and credential headers were written
// into the readable part of every export package.
func TestExportSealsChannelCredentials(t *testing.T) {
	ctx, ch := seedSecretChannel(t)
	src := exportTestRepo(t)
	if err := src.UpdateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	const pw = "export-password-1"

	plain, err := Create(ctx, src, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := Create(ctx, src, ExportOptions{IncludeSecrets: true, Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	for name, pkg := range map[string][]byte{"plain": plain, "keyed": keyed} {
		for _, secret := range []string{"proxy-pass-123", "hdr-secret-456"} {
			if strings.Contains(string(pkg), secret) {
				t.Fatalf("%s export exposes %q in its readable part", name, secret)
			}
		}
		if !strings.Contains(string(pkg), "ua/1") {
			t.Fatalf("%s export dropped a non-secret header", name)
		}
	}

	// Password-protected round trip restores the originals; reserved entries
	// never reach the secret store.
	dst := exportTestRepo(t)
	var imported map[string][]byte
	err = ApplyWithOptions(ctx, keyed, *dst, ApplyOptions{Policy: ConflictOverwrite, Password: pw, SecretImporter: func(_ context.Context, m map[string][]byte) error {
		imported = m
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := dst.GetChannel(ctx, "c1")
	if got.ProxyURL != ch.ProxyURL || got.CustomHeaders["Authorization"] != "Bearer hdr-secret-456" {
		t.Fatalf("keyed import lost credentials: %+v", got)
	}
	for ref := range imported {
		if strings.HasPrefix(ref, channelFieldPrefix) {
			t.Fatalf("reserved export entry %q written to the secret store", ref)
		}
	}

	// A password-less package keeps an existing installation's credentials
	// and never writes the literal placeholder upstream.
	if err := ApplyWithOptions(ctx, plain, *dst, ApplyOptions{Policy: ConflictOverwrite}); err != nil {
		t.Fatal(err)
	}
	got, _ = dst.GetChannel(ctx, "c1")
	if got.ProxyURL != ch.ProxyURL || got.CustomHeaders["Authorization"] != "Bearer hdr-secret-456" {
		t.Fatalf("masked import overwrote existing credentials: %+v", got)
	}
	fresh := exportTestRepo(t)
	_ = fresh.DeleteChannel(ctx, "c1")
	if err := ApplyWithOptions(ctx, plain, *fresh, ApplyOptions{Policy: ConflictOverwrite}); err != nil {
		t.Fatal(err)
	}
	got, _ = fresh.GetChannel(ctx, "c1")
	if v, ok := got.CustomHeaders["Authorization"]; ok {
		t.Fatalf("placeholder header imported into a fresh install: %q", v)
	}
}
