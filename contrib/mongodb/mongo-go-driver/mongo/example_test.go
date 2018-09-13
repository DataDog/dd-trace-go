package mongo_test

import (
	"context"

	"github.com/mongodb/mongo-go-driver/bson"
	"github.com/mongodb/mongo-go-driver/mongo"
	"github.com/mongodb/mongo-go-driver/mongo/clientopt"

	mongotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/mongodb/mongo-go-driver/mongo"
)

func Example() {
	// connect to MongoDB
	client, err := mongo.Connect(context.Background(), "mongodb://localhost:27017",
		clientopt.Monitor(mongotrace.NewMonitor()))
	if err != nil {
		panic(err)
	}
	db := client.Database("example")
	inventory := db.Collection("inventory")

	inventory.InsertOne(context.Background(), bson.NewDocument(
		bson.EC.String("item", "canvas"),
		bson.EC.Int32("qty", 100),
		bson.EC.ArrayFromElements("tags",
			bson.VC.String("cotton"),
		),
		bson.EC.SubDocumentFromElements("size",
			bson.EC.Int32("h", 28),
			bson.EC.Double("w", 35.5),
			bson.EC.String("uom", "cm"),
		),
	))
}
