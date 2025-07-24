# Go URL Shortener

A simple URL shortening service written in Go that converts long URLs into short codes, stores them in a MySQL database or in memory, and provides redirection functionality.

## Purpose

This project was created as a learning exercise to practice and improve Go programming skills, including:

## Features

- HTTP server implementation.
- Working with databases using the database/sql package and MySQL driver.
- Uses mutex locks to ensure safe concurrent access to the data.
- URL validation and short code generation.
- Graceful server shutdown and signal handling.

## Requirements

- Go 1.19 or higher
- MySQL database
- Go MySQL driver (github.com/go-sql-driver/mysql)

## Run the project

### Without database

Move to the right folder 
```bash
cd memorymain
```

Run the project
```bash
go run main.go
```

The server will start on (open in browser)

```bash
localhost:8080
```

### With database

Create the database using the script `db_script.sql` if you want to use the database.

Move to the right folder 
```bash
cd mysqlmain
```

Run the project
```bash
go run main.go
```

The server will start on (open in browser)

```bash
localhost:8080
```
