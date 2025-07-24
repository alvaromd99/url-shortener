package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
)

// Definir la petición y la respuesta como estructuras para que sea mas claro y estructurado
type ShortenRequest struct {
	URL string `json:"url"`
}

type ShortenResponse struct {
	OriginalURL string `json:"originalURL,omitempty"` // omitempty si alguna vez no es necesario
	ShortURL    string `json:"shortURL"`
}

var (
	base62                 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	originalURLToShortCode = make(map[string]string)
	shortCodeToOriginalURL = make(map[string]string)
	mu                     sync.RWMutex // Leer y escribir sin colisiones
	baseURL                = "http://localhost:8080/"
)

// Longitud de los códigos
const codeLength = 5

func generateUniqueCode() string {
	for {
		res := make([]byte, codeLength)
		for i := range codeLength {
			res[i] = base62[rand.Intn(len(base62))]
		}
		code := string(res)

		// Comprobamos que el código generado no exista ya
		mu.RLock()
		_, exists := shortCodeToOriginalURL[code]
		mu.RUnlock()

		// Si no existe lo devuelve y si existe se vuelve a iterar
		if !exists {
			return code
		}
	}
}

func isValidUrl(urlToValidate string) bool {
	u, err := url.ParseRequestURI(urlToValidate)
	if err != nil {
		return false
	}
	// Comprobamos que la url tiene un schema y un host
	if u.Scheme == "" || u.Host == "" {
		return false
	}
	return true
}

// Función para escribir las respuestas en formato json
func writeJSONResponse(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "Error encoding JSON response", http.StatusInternalServerError)
	}
}

func handleShorten(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON payload"})
		return
	}

	if !isValidUrl(req.URL) {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "Invalid URL provided"})
		return
	}

	// Comprobar si la url ya esta almacenada en el map
	mu.RLock()
	existingShortCode, ok := originalURLToShortCode[req.URL]
	mu.RUnlock()

	if ok {
		// Si hemos encontrado la url, devolvemos la url acortada correspondiente
		resp := ShortenResponse{
			OriginalURL: req.URL,
			ShortURL:    baseURL + existingShortCode,
		}
		writeJSONResponse(w, http.StatusOK, resp)
		return
	}

	// Generamos la nueva url acortada
	shortCode := generateUniqueCode()
	shortenedURL := baseURL + shortCode

	// Guardamos la url usando el mutex para evitar problemas
	mu.Lock()
	shortCodeToOriginalURL[shortCode] = req.URL
	originalURLToShortCode[req.URL] = shortCode
	mu.Unlock()

	// Respondemos con la nueva url que hemos creado
	resp := ShortenResponse{
		OriginalURL: req.URL,
		ShortURL:    shortenedURL,
	}
	writeJSONResponse(w, http.StatusCreated, resp)
}

func handleRedirect(w http.ResponseWriter, r *http.Request) {
	// Obtenemos el código de la url
	code := r.PathValue("code")

	// Accedemos al map de forma segura
	mu.RLock()
	originalURL, ok := shortCodeToOriginalURL[code]
	mu.RUnlock()

	// Si no encontramos el código devolvemos un 404
	if !ok {
		notFoundPath := "../static/notFound.html"
		http.ServeFile(w, r, notFoundPath)
		return
	}
	// 301 - StatusFound para decir que lo hemos encontrado correctamente
	http.Redirect(w, r, originalURL, http.StatusFound)
}

func handle404(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "../static/notFound.html")
}

// Home page
func handleHome(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join("../static", "index.html"))
}

func main() {
	// Devolver la pagina html
	http.HandleFunc("/", handleHome)

	// Endpoints para convertir las url
	http.HandleFunc("POST /shorten", handleShorten)
	http.HandleFunc("GET /{code}", handleRedirect)
	http.HandleFunc("GET /404", handle404)

	fmt.Println("Server starting on port :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Printf("Server failed to start: %v\n", err)
	}
}
