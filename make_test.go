package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestMakeRefreshUsesRetainedBaseAndRestoresUser(t *testing.T) {
	type config struct {
		User       string
		Labels     map[string]string
		Entrypoint []string
		Cmd        []string
	}
	type image struct {
		ID           string `json:"Id"`
		Architecture string
		OS           string `json:"Os"`
		Config       config
	}
	checksum := sha256.Sum256(proxyBinary)
	checksumHex := hex.EncodeToString(checksum[:])
	initial := image{ID: "sha256:" + strings.Repeat("a", 64), Architecture: "amd64", OS: "linux", Config: config{User: "65532:65532", Labels: map[string]string{"lambdafy.proxy.checksum": checksumHex}, Entrypoint: []string{"/lambdafy-proxy", "/app"}, Cmd: []string{"arg"}}}
	images := map[string]image{"test-image": initial, initial.ID: initial}
	var mutex sync.Mutex
	var bases []string
	from := regexp.MustCompile(`FROM --platform=linux/amd64 (\S+)`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/images/create"):
			_, _ = io.WriteString(w, "{}\n")
		case strings.HasSuffix(r.URL.Path, "/json"):
			name := strings.TrimSuffix(strings.SplitN(r.URL.Path, "/images/", 2)[1], "/json")
			img, ok := images[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(img)
		case strings.HasSuffix(r.URL.Path, "/tag"):
			source := strings.TrimSuffix(strings.SplitN(r.URL.Path, "/images/", 2)[1], "/tag")
			img, ok := images[source]
			if !ok {
				http.NotFound(w, r)
				return
			}
			repository := strings.TrimPrefix(r.URL.Query().Get("repo"), "docker.io/library/")
			images[repository+":"+r.URL.Query().Get("tag")] = img
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/build"):
			reader := tar.NewReader(r.Body)
			var dockerfile string
			for {
				header, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					http.Error(w, "bad tar", 400)
					return
				}
				if header.Name == "Dockerfile" {
					data, err := io.ReadAll(reader)
					if err != nil {
						t.Error(err)
						return
					}
					dockerfile = string(data)
				}
			}
			match := from.FindStringSubmatch(dockerfile)
			if len(match) != 2 {
				t.Error("missing application base")
				http.Error(w, "bad Dockerfile", 400)
				return
			}
			base, ok := images[match[1]]
			if !ok {
				t.Error("application base was not retained")
				http.Error(w, "missing base", 400)
				return
			}
			if !strings.Contains(dockerfile, "USER 0\nRUN rm -f") || !strings.Contains(dockerfile, "USER 65532:65532\nENTRYPOINT") {
				t.Error("certificate modifications must run as root and restore the application user")
			}
			if !strings.Contains(dockerfile, `ENTRYPOINT ["/lambdafy-proxy","/app"]`) {
				t.Error("entrypoint must have one proxy prefix")
			}
			bases = append(bases, match[1])
			wrapped := base
			wrapped.ID = "sha256:" + strings.Repeat(fmt.Sprint(len(bases)), 64)
			wrapped.Config.Labels = map[string]string{"lambdafy.proxy.checksum": checksumHex, "lambdafy.source.image": match[1]}
			images["test-image"] = wrapped
			images[wrapped.ID] = wrapped
			_, _ = io.WriteString(w, "{}\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	for range 2 {
		if err := lambdafyImage("test-image"); err != nil {
			t.Fatal(err)
		}
	}
	// A new application derived from the previous output must retain its own changes.
	mutex.Lock()
	derived := images["test-image"]
	derived.ID = "sha256:" + strings.Repeat("b", 64)
	images["test-image"] = derived
	images[derived.ID] = derived
	mutex.Unlock()
	for range 2 {
		if err := lambdafyImage("test-image"); err != nil {
			t.Fatal(err)
		}
	}
	mutex.Lock()
	defer mutex.Unlock()
	expected := []string{"lambdafy-base:" + strings.Repeat("a", 64), "lambdafy-base:" + strings.Repeat("a", 64), "lambdafy-base:" + strings.Repeat("b", 64), "lambdafy-base:" + strings.Repeat("b", 64)}
	if fmt.Sprint(bases) != fmt.Sprint(expected) {
		t.Fatalf("unexpected refresh bases: %v", bases)
	}
}
