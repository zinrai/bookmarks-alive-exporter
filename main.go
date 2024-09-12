package main

import (
    "database/sql"
    "flag"
    "log"
    "net/http"
    "sync"
    "time"

    _ "github.com/mattn/go-sqlite3"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
    urlStatus = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bookmark_alive_status",
            Help: "HTTP status code of the bookmarked URL",
        },
        []string{"url"},
    )
    db          *sql.DB
    metricsChan chan metricUpdate
)

type metricUpdate struct {
    url    string
    status float64
}

func init() {
    prometheus.MustRegister(urlStatus)
}

func checkURL(url string) float64 {
    client := &http.Client{
        Timeout: 5 * time.Second,
    }
    resp, err := client.Get(url)
    if err != nil {
        log.Printf("Error checking URL %s: %v", url, err)
        return 0
    }
    defer resp.Body.Close()
    return float64(resp.StatusCode)
}

func urlChecker(urls <-chan string, updates chan<- metricUpdate, wg *sync.WaitGroup) {
    defer wg.Done()
    for url := range urls {
        status := checkURL(url)
        updates <- metricUpdate{url: url, status: status}
    }
}

func collectMetrics(done chan<- bool) {
    start := time.Now()
    rows, err := db.Query("SELECT url FROM bookmarks")
    if err != nil {
        log.Printf("Error querying database: %v", err)
        done <- true
        return
    }
    defer rows.Close()

    urlChan := make(chan string, 100)
    var wg sync.WaitGroup

    // Start worker goroutines
    workerCount := 20
    for i := 0; i < workerCount; i++ {
        wg.Add(1)
        go urlChecker(urlChan, metricsChan, &wg)
    }

    // Send URLs to workers
    urlCount := 0
    go func() {
        for rows.Next() {
            var url string
            if err := rows.Scan(&url); err != nil {
                log.Printf("Error scanning row: %v", err)
                continue
            }
            urlChan <- url
            urlCount++
        }
        close(urlChan)
    }()

    // Wait for all workers to finish
    wg.Wait()
    log.Printf("Collected metrics for %d URLs in %v", urlCount, time.Since(start))
    done <- true
}

func updateMetrics() {
    updatedCount := 0
    for {
        select {
        case update := <-metricsChan:
            urlStatus.WithLabelValues(update.url).Set(update.status)
            updatedCount++
        default:
            log.Printf("Updated %d metrics", updatedCount)
            return // Exit when channel is empty
        }
    }
}

func metricsHandler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        done := make(chan bool)
        go collectMetrics(done)

        // Wait for metrics to be collected (with timeout)
        timeout := time.After(30 * time.Second)
        select {
        case <-timeout:
            log.Println("Metric collection timed out")
        case <-done:
            log.Println("Metric collection completed")
        }

        // Update metrics
        updateMetrics()

        // Serve metrics
        promhttp.Handler().ServeHTTP(w, r)
    }
}

func main() {
    dbPath := flag.String("db", "./bookmarks.db", "Path to SQLite database")
    port := flag.String("port", "8000", "Port to serve metrics on")
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
    log.Printf("Starting server on :%s", *port)
    log.Fatal(http.ListenAndServe(":"+*port, nil))
}
