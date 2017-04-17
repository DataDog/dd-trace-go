package contrib

import "fmt"

var MYSQL_CONFIG = MySQLConfig{
	"ubuntu",
	"",
	"127.0.0.1:3306",
	"circle_test",
}

var POSTGRES_CONFIG = PostgresConfig{
	"ubuntu",
	"",
	"127.0.0.1:5432",
	"circle_test",
}

type Config interface {
	Format() string
}

type Cfg struct {
	User     string
	Password string
	Address  string
	Database string
}

type MySQLConfig Cfg

func (c MySQLConfig) Format() string {
	return fmt.Sprintf("%s:%s@tcp(%s)/%s", c.User, c.Password, c.Address, c.Database)
}

type PostgresConfig Cfg

func (c PostgresConfig) Format() string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", c.User, c.Password, c.Address, c.Database)
}
