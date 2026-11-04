package pianoweb

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMyGoPianoRoutesAvoidIndexRedirect(t *testing.T) {
	handler := http.FileServerFS(MyGoAssets)
	for _, path := range []string{"/mygo-web/piano.html", "/mygo-web/readonly.html", "/mygo-web/assets/piano.js", "/mygo-web/assets/piano.css", "/vendor/gridsound/manifest.json"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d, %s", path, w.Code, w.Header().Get("Location"))
		}
	}
	// The system scheme must not be asked to follow FileServer's special
	// /index.html -> ./ canonical redirect during initial WebView navigation.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/mygo-web/index.html", nil))
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("expected FileServer index redirect, got %d", w.Code)
	}
}
