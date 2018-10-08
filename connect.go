package main

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"io/ioutil"
)

func main() {
	withTLS()
}

func withoutTLS() {
	db, err := sql.Open("mysql", "root:foo@/foo")
	if err != nil {
		panic(err)
	}

	defer db.Close()

	// Open doesn't open a connection. Validate DSN data:
	err = db.Ping()
	if err != nil {
		panic(err.Error()) // proper error handling instead of panic in your app
	}

	fmt.Printf("success without tls\n")
}

func withTLS() {
	rootCertPool := x509.NewCertPool()
	pem, err := ioutil.ReadFile("/tmp/server-ca.pem")
	if err != nil {
		panic(err)
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		panic("Failed to append PEM.")
	}
	mysql.RegisterTLSConfig("custom", &tls.Config{
		RootCAs: rootCertPool,
	})

	db, err := sql.Open("mysql", "root:foo@/foo?tls=custom")
	if err != nil {
		panic(err)
	}

	defer db.Close()

	// Open doesn't open a connection. Validate DSN data:
	err = db.Ping()
	if err != nil {
		panic(err.Error()) // proper error handling instead of panic in your app
	}

	fmt.Printf("success with tls\n")
}

// Execute the query
// rows, err := db.Query("SELECT * FROM table")
// if err != nil {
// 	panic(err.Error()) // proper error handling instead of panic in your app
// }
