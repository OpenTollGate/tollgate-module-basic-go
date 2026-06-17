package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractDomain(t *testing.T) {
	t.Run("SAN first non-empty", func(t *testing.T) {
		cert := &x509.Certificate{DNSNames: []string{"example.com", "www.example.com"}}
		if got := extractDomain(cert); got != "example.com" {
			t.Errorf("got %q, want %q", got, "example.com")
		}
	})

	t.Run("SAN empty falls back to CN", func(t *testing.T) {
		cert := &x509.Certificate{Subject: pkix.Name{CommonName: "fallback.local"}}
		if got := extractDomain(cert); got != "fallback.local" {
			t.Errorf("got %q, want %q", got, "fallback.local")
		}
	})

	t.Run("no SAN no CN returns empty", func(t *testing.T) {
		cert := &x509.Certificate{}
		if got := extractDomain(cert); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("SAN with empty string skips to next", func(t *testing.T) {
		cert := &x509.Certificate{DNSNames: []string{"", "valid.example.com"}}
		if got := extractDomain(cert); got != "valid.example.com" {
			t.Errorf("got %q, want %q", got, "valid.example.com")
		}
	})
}

func TestFilterLines(t *testing.T) {
	t.Run("filters matching lines", func(t *testing.T) {
		input := "line one\nline two match\nline three\nline four match"
		result := filterLines(input, "match")
		if len(result) != 2 {
			t.Fatalf("got %d lines, want 2", len(result))
		}
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		result := filterLines("", "match")
		if len(result) != 0 {
			t.Fatalf("got %d lines, want 0", len(result))
		}
	})

	t.Run("no matches returns empty", func(t *testing.T) {
		result := filterLines("aaa\nbbb\nccc", "zzz")
		if len(result) != 0 {
			t.Fatalf("got %d lines, want 0", len(result))
		}
	})

	t.Run("all match returns all", func(t *testing.T) {
		input := "abc\ndef\nghi"
		result := filterLines(input, "")
		if len(result) != 3 {
			t.Fatalf("got %d lines, want 3", len(result))
		}
	})
}

func TestListContains(t *testing.T) {
	t.Run("value present", func(t *testing.T) {
		if !listContains([]string{"a", "b", "c"}, "b") {
			t.Error("expected true")
		}
	})

	t.Run("value absent", func(t *testing.T) {
		if listContains([]string{"a", "b", "c"}, "d") {
			t.Error("expected false")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		if listContains(nil, "a") {
			t.Error("expected false")
		}
	})

	t.Run("exact match required", func(t *testing.T) {
		if listContains([]string{"abc"}, "ab") {
			t.Error("expected false — substring should not match")
		}
	})
}

func makeTestCertPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.local"},
		DNSNames:     []string{"test.local"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return
}

func makeTestECCertPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "ec.local"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return
}

func TestSplitCombinedPEM(t *testing.T) {
	t.Run("combined cert and key", func(t *testing.T) {
		certPEM, keyPEM := makeTestCertPEM(t)
		combined := append(certPEM, keyPEM...)

		f, err := os.CreateTemp("", "combined-*.pem")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		f.Write(combined)
		f.Close()

		certFile, keyFile, err := splitCombinedPEM(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(filepath.Dir(certFile))

		if certFile == "" || keyFile == "" {
			t.Fatal("expected non-empty file paths")
		}

		certData, err := os.ReadFile(certFile)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(certData)
		if block == nil || block.Type != "CERTIFICATE" {
			t.Error("cert file does not contain CERTIFICATE block")
		}

		keyData, err := os.ReadFile(keyFile)
		if err != nil {
			t.Fatal(err)
		}
		block, _ = pem.Decode(keyData)
		if block == nil || block.Type != "PRIVATE KEY" {
			t.Error("key file does not contain PRIVATE KEY block")
		}
	})

	t.Run("EC private key type recognized", func(t *testing.T) {
		certPEM, keyPEM := makeTestECCertPEM(t)
		combined := append(certPEM, keyPEM...)

		f, err := os.CreateTemp("", "ec-combined-*.pem")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		f.Write(combined)
		f.Close()

		_, _, err = splitCombinedPEM(f.Name())
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("missing cert returns error", func(t *testing.T) {
		_, keyPEM := makeTestCertPEM(t)

		f, err := os.CreateTemp("", "nokey-*.pem")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		f.Write(keyPEM)
		f.Close()

		_, _, err = splitCombinedPEM(f.Name())
		if err == nil {
			t.Fatal("expected error for missing cert")
		}
	})

	t.Run("missing key returns error", func(t *testing.T) {
		certPEM, _ := makeTestCertPEM(t)

		f, err := os.CreateTemp("", "nocert-*.pem")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		f.Write(certPEM)
		f.Close()

		_, _, err = splitCombinedPEM(f.Name())
		if err == nil {
			t.Fatal("expected error for missing key")
		}
	})

	t.Run("no PEM blocks returns error", func(t *testing.T) {
		f, err := os.CreateTemp("", "empty-*.pem")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		f.WriteString("not pem data at all")
		f.Close()

		_, _, err = splitCombinedPEM(f.Name())
		if err == nil {
			t.Fatal("expected error for no PEM blocks")
		}
	})

	t.Run("non-existent file returns error", func(t *testing.T) {
		_, _, err := splitCombinedPEM("/nonexistent/path/file.pem")
		if err == nil {
			t.Fatal("expected error for non-existent file")
		}
	})
}

func TestWritePEM(t *testing.T) {
	t.Run("writes valid PEM", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.pem")
		data := []byte("test data")

		if err := writePEM(path, "CERTIFICATE", data); err != nil {
			t.Fatal(err)
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			t.Fatal("failed to decode PEM")
		}
		if block.Type != "CERTIFICATE" {
			t.Errorf("got type %q, want CERTIFICATE", block.Type)
		}
		if string(block.Bytes) != "test data" {
			t.Errorf("got bytes %q, want %q", string(block.Bytes), "test data")
		}
	})

	t.Run("invalid path returns error", func(t *testing.T) {
		err := writePEM("/nonexistent/dir/test.pem", "CERTIFICATE", []byte("data"))
		if err == nil {
			t.Fatal("expected error for invalid path")
		}
	})
}

func TestFileRead(t *testing.T) {
	t.Run("reads existing file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		os.WriteFile(path, []byte("hello world"), 0644)

		got := fileRead(path)
		if got != "hello world" {
			t.Errorf("got %q, want %q", got, "hello world")
		}
	})

	t.Run("missing file returns empty", func(t *testing.T) {
		got := fileRead("/nonexistent/file.txt")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "ws.txt")
		os.WriteFile(path, []byte("  hello\n"), 0644)

		got := fileRead(path)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})
}

func TestCopyFile(t *testing.T) {
	t.Run("copies contents", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.txt")
		dst := filepath.Join(dir, "dst.txt")
		os.WriteFile(src, []byte("copy me"), 0644)

		if err := copyFile(src, dst); err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "copy me" {
			t.Errorf("got %q, want %q", string(data), "copy me")
		}
	})

	t.Run("missing source returns error", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "dst.txt")
		if err := copyFile("/nonexistent/src.txt", dst); err == nil {
			t.Fatal("expected error for missing source")
		}
	})
}

func TestWriteBackupFile(t *testing.T) {
	t.Run("non-empty writes file with newline", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "backup.txt")
		writeBackupFile(path, "some content")

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "some content\n" {
			t.Errorf("got %q, want %q", string(data), "some content\n")
		}
	})

	t.Run("empty content does not create file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.txt")
		writeBackupFile(path, "")

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("expected file to not exist")
		}
	})
}

func TestSSLInstallCerts(t *testing.T) {
	origSSLDir := sslDir
	origCertDest := certDest
	origKeyDest := keyDest
	defer func() {
		sslDir = origSSLDir
		certDest = origCertDest
		keyDest = origKeyDest
	}()

	dir := t.TempDir()
	sslDir = filepath.Join(dir, "ssl")
	certDest = filepath.Join(sslDir, "server.crt")
	keyDest = filepath.Join(sslDir, "server.key")

	certSrc := filepath.Join(dir, "test.crt")
	keySrc := filepath.Join(dir, "test.key")
	os.WriteFile(certSrc, []byte("cert-data"), 0644)
	os.WriteFile(keySrc, []byte("key-data"), 0600)

	if err := sslInstallCerts(certSrc, keySrc); err != nil {
		t.Fatal(err)
	}

	certData, _ := os.ReadFile(certDest)
	if string(certData) != "cert-data" {
		t.Errorf("cert content mismatch: %q", certData)
	}

	keyData, _ := os.ReadFile(keyDest)
	if string(keyData) != "key-data" {
		t.Errorf("key content mismatch: %q", keyData)
	}

	keyInfo, _ := os.Stat(keyDest)
	if keyInfo.Mode().Perm() != 0600 {
		t.Errorf("key permissions: %o, want 0600", keyInfo.Mode().Perm())
	}

	certInfo, _ := os.Stat(certDest)
	if certInfo.Mode().Perm() != 0644 {
		t.Errorf("cert permissions: %o, want 0644", certInfo.Mode().Perm())
	}
}

func TestCleanupStaleTempDirs(t *testing.T) {
	tmpDir := os.TempDir()
	matchDir := filepath.Join(tmpDir, "tollgate-ssl-test-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	os.MkdirAll(matchDir, 0755)
	defer os.RemoveAll(matchDir)

	splitDir := filepath.Join(tmpDir, "tollgate-ssl-split-test-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	os.MkdirAll(splitDir, 0755)
	defer os.RemoveAll(splitDir)

	keepDir := filepath.Join(tmpDir, "tollgate-keep-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	os.MkdirAll(keepDir, 0755)
	defer os.RemoveAll(keepDir)

	cleanupStaleTempDirs()

	if _, err := os.Stat(matchDir); !os.IsNotExist(err) {
		t.Error("matching temp dir should be removed")
	}
	if _, err := os.Stat(splitDir); !os.IsNotExist(err) {
		t.Error("matching split dir should be removed")
	}
	if _, err := os.Stat(keepDir); os.IsNotExist(err) {
		t.Error("non-matching dir should be kept")
	}
}

func TestSSLStatus_NotConfigured(t *testing.T) {
	origSSLDir := sslDir
	defer func() { sslDir = origSSLDir }()

	sslDir = t.TempDir() + "/nonexistent"

	err := sslStatus()
	if err != nil {
		t.Logf("sslStatus returned: %v (acceptable)", err)
	}
}

func TestConfirmOrYes_Flag(t *testing.T) {
	sslYesFlag = true
	defer func() { sslYesFlag = false }()

	if !confirmOrYes("test") {
		t.Error("expected true when sslYesFlag is set")
	}
}
