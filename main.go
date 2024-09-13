package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	urlStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bookmarks_alive_status",
			Help: "HTTP status code of the bookmarked URL",
		},
		[]string{"url"},
	)
	db          *sql.DB
	metricsChan chan metricUpdate
	userAgent   string
)

type metricUpdate struct {
	url    string
	status float64
}

func init() {
	prometheus.MustRegister(urlStatus)
}

func checkURL(ctx context.Context, url string) float64 {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Printf("Error creating request for URL %s: %v", url, err)
		return 0
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error checking URL %s: %v", url, err)
		return 0
	}
	defer resp.Body.Close()
	return float64(resp.StatusCode)
}

func urlChecker(ctx context.Context, urls <-chan string, updates chan<- metricUpdate, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case url, ok := <-urls:
			if !ok {
				return
			}
			status := checkURL(ctx, url)
			select {
			case <-ctx.Done():
				return
			case updates <- metricUpdate{url: url, status: status}:
			}
		}
	}
}

func collectMetrics(ctx context.Context) error {
	rows, err := db.QueryContext(ctx, "SELECT url FROM bookmarks")
	if err != nil {
		return err
	}
	defer rows.Close()

	urlChan := make(chan string, 100)
	var wg sync.WaitGroup

	workerCount := 20
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go urlChecker(ctx, urlChan, metricsChan, &wg)
	}

	go func() {
		defer close(urlChan)
		for rows.Next() {
			var url string
			if err := rows.Scan(&url); err != nil {
				log.Printf("Error scanning row: %v", err)
				continue
			}
			select {
			case <-ctx.Done():
				return
			case urlChan <- url:
			}
		}
	}()

	wg.Wait()
	return nil
}

func updateMetrics(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-metricsChan:
			if !ok {
				return
			}
			urlStatus.WithLabelValues(update.url).Set(update.status)
		default:
			return // Exit when channel is empty
		}
	}
}

func metricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		if err := collectMetrics(ctx); err != nil {
			log.Printf("Error collecting metrics: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		updateMetrics(ctx)

		promhttp.Handler().ServeHTTP(w, r)
	}
}

func main() {
	dbPath := flag.String("db", "./bookmarks.db", "Path to SQLite database")
	port := flag.String("port", "8000", "Port to serve metrics on")
	flag.StringVar(&userAgent, "user-agent", "bookmarks-alive-exporter/1.0", "User Agent string to use for HTTP requests")
	flag.Parse()

	var err error
	db, err = sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}

	metricsChan = make(chan metricUpdate, 1000)

	http.Handle("/metrics", metricsHandler())
	log.Printf("Starting bookmarks-alive-exporter on :%s", *port)
	log.Printf("Using User-Agent: %s", userAgent)

	server := &http.Server{
		Addr:    ":" + *port,
		Handler: nil,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exiting")
}
