package horoscope

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetHoroscope_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sign") != "1" {
			t.Errorf("Expected sign parameter '1', got '%s'", r.URL.Query().Get("sign"))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<div class="main-horoscope"><p>First paragraph</p><p><strong>Today:</strong> - Your day will be great!</p></div>`))
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscope, err := GetHoroscope("belier")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "Your day will be great!"
	if horoscope != expected {
		t.Errorf("Expected horoscope '%s', got '%s'", expected, horoscope)
	}
}

func TestGetHoroscope_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	_, err := GetHoroscope("belier")
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

func TestGetHoroscope_InvalidHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<div class="main-horospace"></div>`))
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscope, err := GetHoroscope("belier")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if horoscope != "" {
		t.Errorf("Expected empty horoscope for invalid HTML, got '%s'", horoscope)
	}
}

func TestGetHoroscope_MultipleLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<div class="main-horoscope"><p>First paragraph</p><p><strong>Lucky Number:</strong> - 7\n\nYour day is bright.</p></div>`))
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscope, err := GetHoroscope("belier")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "7\\n\\nYour day is bright."
	if horoscope != expected {
		t.Errorf("Expected horoscope '%s', got '%s'", expected, horoscope)
	}
}

func TestGetHoroscope_BoldingRemoved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<div class="main-horoscope"><p>First paragraph</p><p><strong>Focus:</strong> - Work on your goals.</p></div>`))
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscope, err := GetHoroscope("belier")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if horoscope == "Focus: Work on your goals." {
		t.Errorf("Expected bold text to be removed, but got '%s'", horoscope)
	}

	expected := "Work on your goals."
	if horoscope != expected {
		t.Errorf("Expected horoscope '%s', got '%s'", expected, horoscope)
	}
}

func TestGetHoroscopes_AllSigns(t *testing.T) {
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<div class="main-horoscope"><p>First paragraph</p><p><strong>Today:</strong> - Test horoscope</p></div>`))
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscopes, errors := GetHoroscopes()

	if errors == nil {
		t.Fatal("Expected errors map, got nil")
	}

	if horoscopes == nil {
		t.Fatal("Expected horoscopes map, got nil")
	}

	totalRequests := len(Signes)
	if requestCount != totalRequests {
		t.Errorf("Expected %d requests, got %d", totalRequests, requestCount)
	}

	signCount := 0
	horoscopes.Range(func(key, value interface{}) bool {
		signCount++
		return true
	})

	if signCount != totalRequests {
		t.Errorf("Expected %d horoscopes, got %d", totalRequests, signCount)
	}
}

func TestGetHoroscopes_WithErrors(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount%2 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<div class="main-horoscope"><p>First paragraph</p><p><strong>Today:</strong> - Test</p></div>`))
		}
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscopes, errors := GetHoroscopes()

	var successCount, errorCount int
	horoscopes.Range(func(key, value interface{}) bool {
		successCount++
		return true
	})

	errors.Range(func(key, value interface{}) bool {
		errorCount++
		return true
	})

	total := len(Signes)
	if successCount+errorCount != total {
		t.Errorf("Expected total %d, got %d (success: %d, error: %d)", total, successCount+errorCount, successCount, errorCount)
	}

	if successCount == 0 || errorCount == 0 {
		t.Error("Expected both successful and failed horoscope fetches")
	}
}

func TestGetHoroscope_WhitespaceTrimmed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<div class="main-horoscope"><p>First paragraph</p><p><strong>Today:</strong> -   Trimmed text   </p></div>`))
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscope, err := GetHoroscope("belier")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if horoscope == "" {
		t.Error("Expected non-empty horoscope after trimming")
	}

	if len(horoscope) < 10 {
		t.Errorf("Expected trimmed horoscope with content, got '%s' (len: %d)", horoscope, len(horoscope))
	}
}

func TestGetHoroscope_InvalidSign(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<div class="main-horoscope"><p><strong>Today:</strong> Test</p></div>`))
	}))
	defer server.Close()

	originalURL := url
	url = server.URL + "?sign="
	defer func() { url = originalURL }()

	horoscope, err := GetHoroscope("invalid-sign")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if horoscope != "" {
		t.Errorf("Expected empty horoscope for invalid sign, got '%s'", horoscope)
	}
}
