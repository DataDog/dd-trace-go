package contrib

import "fmt"

type MysqlConfig struct {
	User     string
	Password string
	Address  string
	Database string
}

func (mc *MysqlConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s)/%s", mc.User, mc.Password, mc.Address, mc.Database)
}

var MYSQL_CONFIG = MysqlConfig{
	"ubuntu",
	"",
	"127.0.0.1:3306",
	"circle_test",
}
