package datastore

type License struct {
	Id int64
	CompanyId int
	Jwt string
}

func (db *DB) AllLicenses(companyId int) ([]*License, error) {
	rows, err := db.Query("SELECT id, jwt FROM licenses WHERE company_id = ?", companyId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	licenses := make([]*License, 0)
	for rows.Next() {
		lic := &License{}
		err := rows.Scan(&lic.Id, &lic.Jwt)
		if err != nil {
			return nil, err
		}
		lic.CompanyId = companyId
		licenses = append(licenses, lic)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return licenses, nil
}

func (db *DB) GetLicenseById(id int) (*License, error) {
	lic := &License{}
	row := db.QueryRow("SELECT id, jwt FROM licenses WHERE id = ?", id)
	err := row.Scan(&lic.Id, &lic.Jwt)
	if err != nil {
		return nil, err
	}
	return lic, nil
}

func (db *DB) CreateLicense(license *License) (*License, error) {
	res, err := db.Exec("INSERT INTO licenses (company_id, jwt) VALUES (?, ?)", license.CompanyId, license.Jwt)
	if err != nil {
		return nil, err
	}
	license.Id, err = res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return license, nil
}

