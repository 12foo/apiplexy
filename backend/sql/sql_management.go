package sql

import (
	gosql "database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/12foo/apiplexy"
	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jinzhu/gorm"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

type sqlDBKey struct {
	KeyID     string `sql:"primary_key" gorm:"primary_key"`
	Realm     string
	Type      string
	Data      string
	Quota     string
	User      string `sql:"not null;index"`
	CreatedAt time.Time
	DeletedAt *time.Time
}

func (k *sqlDBKey) toKey() *apiplexy.Key {
	ck := apiplexy.Key{
		ID:    k.KeyID,
		Realm: k.Realm,
		Type:  k.Type,
		Quota: k.Quota,
	}
	json.Unmarshal([]byte(k.Data), &ck.Data)
	return &ck
}

func (k *sqlDBKey) TableName() string {
	return "api_keys"
}

type sqlDBUser struct {
	Email     string `sql:"type:varchar(100);primary_key" gorm:"primary_key"`
	Name      string
	Password  []byte `sql:"not null"`
	Admin     bool
	Active    bool
	Profile   string
	CreatedAt time.Time
	DeletedAt *time.Time
	LastLogin *time.Time
}

func (u *sqlDBUser) TableName() string {
	return "api_users"
}

func (u *sqlDBUser) toUser() *apiplexy.User {
	cu := apiplexy.User{
		Email:  u.Email,
		Name:   u.Name,
		Active: u.Active,
	}
	json.Unmarshal([]byte(u.Profile), &cu.Profile)
	return &cu
}

type SQLDBBackend struct {
	db gorm.DB
}

func (sql *SQLDBBackend) GetKey(keyId string, keyType string) (*apiplexy.Key, error) {
	k := sqlDBKey{}
	if sql.db.Where(sqlDBKey{KeyID: keyId, Type: keyType}).First(&k).RecordNotFound() {
		return nil, nil
	}
	return k.toKey(), nil
}

func (sql *SQLDBBackend) AddUser(email string, password string, user *apiplexy.User) error {
	cu := sqlDBUser{}
	if !sql.db.Where(&sqlDBUser{Email: email}).First(&cu).RecordNotFound() {
		return fmt.Errorf("A user with that email already exists.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if user.Profile == nil {
		user.Profile = make(map[string]interface{})
	}
	bprofile, err := json.Marshal(user.Profile)
	if err != nil {
		return err
	}

	u := sqlDBUser{
		Email:    email,
		Name:     user.Name,
		Password: hash,
		Profile:  string(bprofile[:]),
	}
	if err := sql.db.Create(&u).Error; err != nil {
		return err
	}
	return nil
}

func (sql *SQLDBBackend) GetUser(email string) *apiplexy.User {
	u := sqlDBUser{}
	if sql.db.Where(&sqlDBUser{Email: email}).First(&u).RecordNotFound() {
		return nil
	}
	return u.toUser()
}

func (sql *SQLDBBackend) ActivateUser(email string) error {
	u := sqlDBUser{}
	if sql.db.Where(&sqlDBUser{Email: email}).First(&u).RecordNotFound() {
		return fmt.Errorf("User not found.")
	}
	sql.db.Model(&u).Where(&sqlDBUser{Email: email}).UpdateColumns(sqlDBUser{Active: true})
	return nil
}

func (sql *SQLDBBackend) ResetPassword(email string, newPassword string) error {
	u := sqlDBUser{}
	if sql.db.Where(&sqlDBUser{Email: email}).First(&u).RecordNotFound() {
		return fmt.Errorf("User not found.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	sql.db.Model(&u).Where(&sqlDBUser{Email: email}).UpdateColumns(sqlDBUser{Password: hash})
	return nil
}

func (sql *SQLDBBackend) UpdateUser(email string, user *apiplexy.User) error {
	u := sqlDBUser{}
	if sql.db.Where(&sqlDBUser{Email: email}).First(&u).RecordNotFound() {
		return fmt.Errorf("User not found.")
	}
	bp, _ := json.Marshal(user.Profile)
	sql.db.Model(&u).Where(&sqlDBUser{Email: email}).UpdateColumns(sqlDBUser{Name: user.Name, Profile: string(bp[:])})
	return nil
}

func (sql *SQLDBBackend) Authenticate(email string, password string) *apiplexy.User {
	u := sqlDBUser{}
	if sql.db.Where(&sqlDBUser{Email: email, Active: true}).First(&u).RecordNotFound() {
		return nil
	}
	if bcrypt.CompareHashAndPassword(u.Password, []byte(password)) != nil {
		return nil
	}
	return u.toUser()
}

func (sql *SQLDBBackend) AddKey(email string, key *apiplexy.Key) error {
	u := sqlDBUser{}
	if sql.db.Where(&sqlDBUser{Email: email}).First(&u).RecordNotFound() {
		return fmt.Errorf("User not found.")
	}
	bd, _ := json.Marshal(key.Data)
	k := sqlDBKey{
		KeyID: key.ID,
		Realm: key.Realm,
		Type:  key.Type,
		Quota: key.Quota,
		Data:  string(bd[:]),
		User:  email,
	}
	sql.db.Create(&k)
	return nil
}

func (sql *SQLDBBackend) DeleteKey(email string, keyID string) error {
	k := sqlDBKey{}
	if sql.db.Where(sqlDBKey{KeyID: keyID}).First(&k).RecordNotFound() {
		return fmt.Errorf("Key does not exist.")
	}
	if k.User != email {
		return fmt.Errorf("You are not the owner of this key.")
	}
	sql.db.Delete(&k)
	return nil
}

func (sql *SQLDBBackend) GetAllKeys(email string) ([]*apiplexy.Key, error) {
	ks := []sqlDBKey{}
	if sql.db.Find(&ks, sqlDBKey{User: email}).Error != nil {
		return nil, nil
	}
	cks := make([]*apiplexy.Key, len(ks))
	for i, k := range ks {
		cks[i] = k.toKey()
	}
	return cks, nil
}

func (sql *SQLDBBackend) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"driver":            strings.Join(gosql.Drivers(), "/"),
		"connection_string": "host=localhost port=5432 user=apiplexy password=apiplexy dbname=apiplexy",
		"create_tables":     false,
	}
}

func (sql *SQLDBBackend) Configure(config map[string]interface{}) error {
	db, err := gorm.Open(config["driver"].(string), config["connection_string"].(string))

	if err != nil {
		return fmt.Errorf("Error connecting to database. %s", err.Error())
	}

	if config["create_tables"].(bool) {
		if err = db.CreateTable(&sqlDBUser{}).Error; err != nil {
			return fmt.Errorf("Error creating user table: %s", err.Error())
		}
		if err = db.CreateTable(&sqlDBKey{}).Error; err != nil {
			return fmt.Errorf("Error creating key table: %s", err.Error())
		}
	}

	sql.db = db

	return nil
}

func init() {
	// _ = apiplexy.ManagementBackendPlugin(&SQLDBBackend{})
	apiplexy.RegisterPlugin(
		"sql-full",
		"Use popular SQL databases as backend stores (with full user/key management).",
		"https://github.com/12foo/apiplexy/tree/master/backend/sql",
		SQLDBBackend{},
	)
}
