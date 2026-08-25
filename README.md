# findcopies

`findcopies` is a command-line tool that searches directories for files matching a query file by
content hash.

## Prerequisites

- Go 1.26 or later (only needed to build from source)

## Installation

```bash
go install github.com/audivir/findcopies@latest
```

Or build manually:

```bash
git clone https://github.com/audivir/findcopies
cd findcopies
go build .
```

## Usage

```bash
# Search the current directory for copies of query.txt
findcopies --query query.txt

# Search specific directories
findcopies --query query.txt /path/to/dir1 /path/to/dir2

# Restrict to specific file patterns
findcopies --query query.txt --include "*.jpg" --exclude "*_thumb.jpg"

# Control the number of parallel workers
findcopies --query query.txt --workers 4

# Print errors and skipped files to stderr
findcopies --query query.txt --verbose

# Help
findcopies --help
```

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
