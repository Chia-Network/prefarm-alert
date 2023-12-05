package mysql

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Datastore stores the audit data in the database
type Datastore struct {
	singletonName string

	dbHost string
	dbPort uint16
	dbUser string
	dbPass string
	dbName string

	mysqlClient *sql.DB
}

// NewDatastore returns a new datastore for storing audit state in mysql
func NewDatastore(singletonName string, dbHost string, dbPort uint16, dbUser string, dbPass string, dbName string) (*Datastore, error) {
	var err error

	datastore := &Datastore{
		singletonName: singletonName,
		dbHost:        dbHost,
		dbPort:        dbPort,
		dbUser:        dbUser,
		dbPass:        dbPass,
		dbName:        dbName,
	}

	err = datastore.createDBClient()
	if err != nil {
		return nil, err
	}

	err = datastore.initTables()
	if err != nil {
		return nil, err
	}

	return datastore, nil
}

func (d *Datastore) createDBClient() error {
	var err error

	cfg := mysql.Config{
		User:                 d.dbUser,
		Passwd:               d.dbPass,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%d", d.dbHost, d.dbPort),
		DBName:               d.dbName,
		AllowNativePasswords: true,
	}
	d.mysqlClient, err = sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}

	d.mysqlClient.SetConnMaxLifetime(time.Minute * 3)
	d.mysqlClient.SetMaxOpenConns(10)
	d.mysqlClient.SetMaxIdleConns(10)

	return nil
}

// initTables ensures that the tables required exist and have the correct columns present
func (d *Datastore) initTables() error {
	query := "CREATE TABLE IF NOT EXISTS `auditdata` (" +
		"  `id` int unsigned NOT NULL AUTO_INCREMENT," +
		"  `singleton` VARCHAR(255) NOT NULL," +
		"  `auditjson` TEXT NOT NULL," +
		"  PRIMARY KEY (`id`)," +
		"  UNIQUE KEY `singleton-unique` (`singleton`)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;"

	result, err := d.mysqlClient.Query(query)
	if err != nil {
		return err
	}
	return result.Close()
}

// GetAuditData returns the audit data from the database
func (d *Datastore) GetAuditData() (string, error) {
	rows, err := d.mysqlClient.Query("SELECT auditjson FROM auditdata where singleton = ?", d.singletonName)
	if err != nil {
		return "", err
	}

	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Printf("Could not close rows: %s\n", err.Error())
		}
	}(rows)

	var (
		auditjson string
	)

	// If there are no rows, return an empty list of audit events to bootstrap the json with
	if !rows.Next() {
		return "[]", nil
	}

	err = rows.Scan(&auditjson)
	if err != nil {
		return "", err
	}

	return auditjson, nil
}

// StoreAuditData stores the latest audit json in the DB for this singleton
func (d *Datastore) StoreAuditData(data string) error {
	_, err := d.mysqlClient.Query("INSERT INTO auditdata (singleton, auditjson) VALUES (?, ?)"+
		" ON DUPLICATE KEY UPDATE auditjson = VALUES(auditjson);", d.singletonName, data)
	return err
}
