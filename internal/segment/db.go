package segment

import "database/sql"

func Fetch(db *sql.DB) (start, end int64, err error) {
	tx, _ := db.Begin()

	var max, step int64
	tx.QueryRow("SELECT max_id, step FROM id_segment WHERE biz_tag='order' FOR UPDATE").
		Scan(&max, &step)

	newMax := max + step
	tx.Exec("UPDATE id_segment SET max_id=?", newMax)

	tx.Commit()

	return max, newMax, nil
}