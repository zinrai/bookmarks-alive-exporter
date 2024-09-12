# bookmarks-alive-exporter

The bookmarks-alive-exporter is a Prometheus exporter designed to monitor the availability of URLs stored in a [bookmark-manager](https://github.com/zinrai/bookmark-manager). It provides metrics on the HTTP status of each bookmarked URL, enabling easy monitoring and alerting on the health of saved web resources.

## Features

- Reads bookmark URLs from a SQLite database
- Asynchronously checks the availability of each URL
- Exports HTTP status codes as Prometheus metrics
- Collects metrics on-demand (when /metrics endpoint is requested)
- Configurable via command-line flags (database path, port number)

## Installation

```bash
$ go build
```

## Usage

1. Start the exporter from the command line:

   ```bash
   ./bookmarks-alive-exporter -db /path/to/your/bookmarks.db -port 8080
   ```

   - `-db`: Path to the SQLite database file (default: "./bookmarks.db")
   - `-port`: Port on which the exporter will listen (default: "8000")

2. Add the following to your Prometheus configuration file (usually `prometheus.yml`):

   ```yaml
   scrape_configs:
     - job_name: 'bookmarks_alive'
       static_configs:
         - targets: ['localhost:8080']
   ```

3. Restart Prometheus and verify that the new target has been added.

## Metrics

- `bookmarks_alive_status{url="..."}`：HTTP status code of the URL. A value of 0 indicates an error (e.g., connection failure).

## License

This project is licensed under the MIT License - see the [LICENSE](https://opensource.org/license/mit) for details.
