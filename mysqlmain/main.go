package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Definir la petición y la respuesta como estructuras para que sea mas claro y estructurado
type ShortenRequest struct {
	URL string `json:"url"`
}
type ShortenResponse struct {
	OriginalURL string `json:"originalURL,omitempty"` // omitempty si alguna vez no es necesario
	ShortURL    string `json:"shortURL"`
}

// Para guardar las configuraciones de la base de datos a nivel servicio
type Config struct {
	BaseURL      string
	NotFoundPath string
	IndexPath    string
}

type App struct {
	DB   *sql.DB
	Conf Config
}

var (
	base62      = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	maxAttempts = 20
)

// Longitud de los códigos
const codeLength = 5

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

func (app *App) handle404(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, app.Conf.NotFoundPath)
}

// Home page
func (app *App) handleHome(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, app.Conf.IndexPath)
}

func (app *App) handleRedirectWithDB(w http.ResponseWriter, r *http.Request) {
	// Obtenemos el código de la url
	code := r.PathValue("code")
	// Para guardar la url original
	var redirectUrl string

	row := app.DB.QueryRow("SELECT original_url FROM urls WHERE short_url = ?", code)
	if err := row.Scan(&redirectUrl); err != nil {
		// Comprobamos si el error es que no ha encontrado ninguna fila
		if err == sql.ErrNoRows {
			log.Printf("Info: Short URL '%s' not found.", code)
			http.ServeFile(w, r, app.Conf.NotFoundPath)
			return
		}
		log.Printf("Error: Database error retrieving short URL '%s': %v", code, err)
		writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	// 301 - Moved permanently para decir que ha sido movido a una nueva url (es eficiente para el buscador y el SEO)
	http.Redirect(w, r, redirectUrl, http.StatusMovedPermanently)
}

func generateUniqueCodeWithDB(db *sql.DB) (string, error) {
	// Poner un limite para evitar bucles infinitos
	for i := range maxAttempts {
		res := make([]byte, codeLength)
		for i := range codeLength {
			res[i] = base62[rand.Intn(len(base62))]
		}
		code := string(res)

		var exists int
		// Comprobamos que el código generado no exista ya
		row := db.QueryRow("SELECT 1 FROM urls WHERE short_url = ?", code)
		if err := row.Scan(&exists); err != nil {
			// Check if error is because the is no rows found with that id
			if err == sql.ErrNoRows {
				return code, nil
			}
			// Si ocurre otro error en la base de datos
			log.Printf("Error: Database error during code generation uniqueness check (attempt %d): %v", i+1, err)
			return "", fmt.Errorf("database error checking code uniqueness: %w", err)
		}
		// Log por si hay problemas y hay que hacer debug
		log.Printf("Info: Generated code '%s' already exists, trying again (attempt %d).", code, i+1)
	}
	// Hemos agotado todos los intentos y no ha podido generar un código único
	return "", fmt.Errorf("failed to generate unique code after %d attempts", maxAttempts)
}

func (app *App) handleShortenWithDB(w http.ResponseWriter, r *http.Request) {
	var req ShortenRequest
	// Obtenemos la URL del body de la petición http
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON payload"})
		return
	}

	// Comprobamos la validez de la URL
	if !isValidUrl(req.URL) {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "Invalid URL provided"})
		return
	}

	var shortCode string
	// Intentamos buscar el código para esa URL en la base de datos
	err := app.DB.QueryRow("SELECT short_url FROM urls WHERE original_url = ?", req.URL).Scan(&shortCode)

	if err != nil {
		// Si el error es distinto de sql.ErrNoRows ha habido un problema con la base de datos
		if err != sql.ErrNoRows {
			log.Printf("Error: Database error checking for existing URL '%s': %v", req.URL, err)
			writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
			return
		}

		// Si el error es sql.ErrNoRows significa que debemos generar un nuevo código para esa URL
		// Vamos a intentar generar el código 5 veces para prevenir loops infinitos
		for attempts := 0; attempts < 5; attempts++ {
			newCode, genErr := generateUniqueCodeWithDB(app.DB)
			// No hemos podido obtener un código único a pesar de los intentos en la función de generateUniqueCodeWithD
			if genErr != nil {
				log.Printf("Error: Failed to generate unique code after %d attempts: %v", attempts+1, genErr)
				writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate short URL code"})
				return
			}

			_, insertErr := app.DB.Exec("INSERT INTO urls (original_url, short_url) VALUES (?, ?)", req.URL, newCode)
			// Si no ha habido errores significa que todo ha ido bien y salimos del bucle guardando ese nuevo código
			if insertErr == nil {
				shortCode = newCode
				break
			}

			// Comprobamos si el error es el de clave duplicada (MySQL error 1062)
			if mysqlErr, ok := insertErr.(*mysql.MySQLError); ok && mysqlErr.Number == 1062 {
				log.Printf("Warning: Duplicate short URL code '%s' during insert, retrying (attempt %d). Error: %v", newCode, attempts+1, insertErr)
				// Significa que hay colisiones con algún código de la base de datos
				// Pasamos al siguiente intento
				continue
			}

			// Si ha habido otro error inesperado en la base de datos
			log.Printf("ERROR: Database error inserting new URL '%s' with code '%s' (attempt %d): %v", req.URL, newCode, attempts+1, insertErr)
			writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": "Failed to store URL"})
			return
		}

		// Si hemos gastado todos los intentos de insertar el nuevo código en la base de datos
		if shortCode == "" {
			log.Printf("CRITICAL: Exceeded retry attempts to generate and insert unique short URL for '%s'.", req.URL)
			writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate and store unique short URL after multiple attempts"})
			return
		}

	}
	// En este punto el código es uno obtenido de la base de datos o es el que hemos generado nuevo
	resp := ShortenResponse{
		OriginalURL: req.URL,
		ShortURL:    app.Conf.BaseURL + shortCode,
	}
	writeJSONResponse(w, http.StatusCreated, resp)
}

func main() {
	// Configuración para la base de datos
	cfg := mysql.NewConfig()
	cfg.User = os.Getenv("DBUSER")
	cfg.Passwd = os.Getenv("DBPASS")
	cfg.Net = "tcp"
	cfg.Addr = "127.0.0.1:3306"
	cfg.DBName = "BDurls"

	// Abrir la conexión de la base de datos
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		// Log los errores cuando nos conectamos a la base de datos
		log.Fatalf("Error opening database connection: %v", err)
	}

	// Comprobar la conexión a la base de datos
	if err := db.Ping(); err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}

	// Inicializar la app
	app := &App{
		DB: db,
		Conf: Config{
			BaseURL:      "http://localhost:8080/",
			NotFoundPath: "../static/notFound.html",
			IndexPath:    "../static/index.html",
		},
	}

	mux := http.NewServeMux()

	// Devolver la pagina html
	mux.HandleFunc("/", app.handleHome)

	// Endpoints para convertir las url
	mux.HandleFunc("POST /shorten", app.handleShortenWithDB)
	mux.HandleFunc("GET /{code}", app.handleRedirectWithDB)
	mux.HandleFunc("GET /404", app.handle404)

	// Preparamos el servidor http
	srv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Iniciar el servidor en una go routine
	// Esto permite que el main thread continúe ejecutándose para manejar señales.
	go func() {
		log.Println("Server starting on port :8080...")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// http.ErrServerClosed es el error esperado cuando el servidor se apaga elegantemente.
			// Otros errores son problemáticos.
			log.Fatalf("Server failed to start: %v\n", err)
		}
	}()

	// Configurar el canal para recibir señales del sistema
	// Esto nos permitirá saber cuándo el sistema operativo (o algo como Docker)
	// nos pide que nos apaguemos.
	quit := make(chan os.Signal, 1)
	// Registramos para recibir señales de interrupción (Ctrl+C) y terminación (kill).
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Bloquear hasta que se reciba una señal
	// El main goroutine se detendrá aquí hasta que algo se envíe al canal 'quit'.
	<-quit
	log.Println("Shutting down server... Received shutdown signal.")

	// Iniciar el apagado elegante del servidor HTTP
	// Creamos un contexto con un timeout para dar tiempo a que las peticiones en curso terminen.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 5 segundos de gracia
	defer cancel()                                                          // Asegura que el recurso del contexto se libere

	// srv.Shutdown() intenta apagar el servidor elegantemente:
	// - Deja de aceptar nuevas conexiones.
	// - Espera a que las conexiones existentes terminen o hasta que el contexto expire.
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown due to error: %v", err)
	}

	// Cerrar la conexión a la base de datos
	// Esto se hace *después* de que el servidor HTTP se haya apagado,
	// para que las funciones de handler puedan seguir usando la DB durante el apagado.
	if err := db.Close(); err != nil {
		log.Printf("Error: Failed to close database connection: %v", err)
	} else {
		log.Println("Database connection closed.")
	}

	log.Println("Server exited gracefully.")
}
