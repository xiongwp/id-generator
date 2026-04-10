package segment

import "database/sql"

func Fetch(db *sql.DB) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var maxID int64
	err = tx.QueryRow("SELECT max_id FROM segment LIMIT 1").Scan(&maxID)
	if err != nil {
		return 0, err
	}

	err = tx.Commit()
	if err != nil {
		return 0, err
	}

	return maxID, nil
}
