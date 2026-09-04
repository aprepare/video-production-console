package main

import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	db, err := sql.Open("sqlite3", "video-console-data/console.db?_busy_timeout=5000")
	if err != nil { panic(err) }
	defer db.Close()
	res, err := db.Exec(`UPDATE projects SET title=? WHERE id=?`, "多模型并行测试", "4b8f56c2-d80b-4a02-a51d-fa636a6492e1")
	if err != nil { panic(err) }
	n, _ := res.RowsAffected()
	fmt.Println("updated", n)
}
