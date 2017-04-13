package contrib

import "fmt"

type MysqlConfig struct {
	RootPassword string
	Password     string
	User         string
	Database     string
	Address      string
}

func (mc *MysqlConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s)/%s", mc.User, mc.Password, mc.Address, mc.Database)
}

var MYSQL_CONFIG = MysqlConfig{
	"admin",
	"test",
	"test",
	"test",
	"127.0.0.1:3306",
}
