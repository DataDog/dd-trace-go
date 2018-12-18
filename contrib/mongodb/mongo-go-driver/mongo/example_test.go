package mongo_test

import (
	"context"

	"github.com/mongodb/mongo-go-driver/bson"
	"github.com/mongodb/mongo-go-driver/mongo"
	"github.com/mongodb/mongo-go-driver/mongo/options"

	mongotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/mongodb/mongo-go-driver/mongo"
)

func Example() {
	// connect to MongoDB
	clientOptions := options.Client()
	clientOptions.SetMonitor(mongotrace.NewMonitor())
	client, err := mongo.Connect(context.Background(), "mongodb://localhost:27017", clientOptions)
	if err != nil {
		panic(err)
	}
	db := client.Database("example")
	inventory := db.Collection("inventory")

	inventory.InsertOne(context.Background(), bson.D{
		{Key: "item", Value: "canvas"},
		{Key: "qty", Value: 100},
		{Key: "tags", Value: bson.A{"cotton"}},
		{Key: "size", Value: bson.D{
			{Key: "h", Value: 28},
			{Key: "w", Value: 35.5},
			{Key: "uom", Value: "cm"},
		}},
	})
}
