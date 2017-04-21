package contrib

import "fmt"

type Config struct {
	Template string
	User     string
	Password string
	Host     string
	Port     string
	DBName   string
}

func (c Config) DSN() string {
	return fmt.Sprintf(c.Template, c.User, c.Password, c.Host, c.Port, c.DBName)
}

var MYSQL_CONFIG = Config{
	"%s:%s@tcp(%s:%s)/%s",
	"ubuntu",
	"",
	"127.0.0.1",
	"3306",
	"circle_test",
}

var POSTGRES_CONFIG = Config{
	"postgres://%s:%s@%s:%s/%s?sslmode=disable",
	"ubuntu",
	"",
	"127.0.0.1",
	"5432",
	"circle_test",
}
