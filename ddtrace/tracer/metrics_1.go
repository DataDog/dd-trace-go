package tracer

// Dummy file to store my implementation trying to report the metrics from the tracer

// At first I tried to cache the `dbs` so we don't have to check globalconfig at every interval,
// but this assumes that the time elapsed between tracer.Start() and sqltrace.Open(WithDBStats())
// is less than or equal to `interval`, for all dbs instrumented with sqltrace.Open(WithDBStats())
// func (t *tracer) reportDBStats(interval time.Duration) {
// 	tick := time.NewTicker(interval)
// 	defer tick.Stop()
// 	for {
// 		select {
// 		case <- tick.C:
// 			log.Debug("Attempting to pull DB stats...")
// 			if dbs := globalconfig.DBs(); dbs == nil {
// 				log.Debug("No traced DB connection found; cannot pull DB stats.")
// 			} else {
// 				log.Debug("Traced DB connection found: reporting DB stats.")
// 				for _, db := range dbs {
// 					stats := db.Stats()
// 					openConns := stats.OpenConnections
// 					// might want to add tags to this....
// 					t.statsd.Gauge("sql.db.open_connections", float64(openConns), nil, 1)
// 				}
// 			}
// 		case <-t.stop:
// 			return
// 		}
// 	}
// }

// Here is the caching implementation, which assumes the following:
// --> that the database/sql contrib pkg will be loaded in the application before the interval elapses
// --> that all dbs registered with WithDBStats option are loaded before interval elapses
// The tracer runs first and then the contribs... In other words, this could break if the interval is shorter
// than the time elapsed in application launch between tracer.Start() and sqltrace.OpenDB(WtihDBStats())

// func (t *tracer) reportDBStatsCaching(interval time.Duration) {
// 	tick := time.NewTicker(interval)
// 	defer tick.Stop()
// 	var dbs []*sql.DB
// 	for {
// 		select {
// 		case <- tick.C:
// 			log.Debug("Attempting to pull DB stats...")
// 			// if it's nil, either we need to set it for the first time or the contrib hasn't loaded yet
// 			// although this assumes that either none or all of the contribs have loaded and doesn't consider the case where some contrib(s) may
// 			if dbs == nil {
// 				// assumes all cases of sqltrace.OpenDB(WithDBStats()) have run already
// 				dbs = globalconfig.DBs()
// 			}
// 			if len(dbs) > 0 {
// 				log.Debug("Traced DB connection found: reporting DB stats.")
// 				for _, db := range dbs {
// 					stats := db.Stats()
// 					openConns := stats.OpenConnections
// 					// might want to add tags to this....
// 					t.statsd.Gauge("sql.db.open_connections", float64(openConns), nil, 1)
// 				}
// 			} else {
// 				// if it's still nil, there's nothing to pull from
// 				log.Debug("No traced DB connection found; cannot pull DB stats.")

// 			}
// 		case <-t.stop:
// 			return
// 		}
// 	}
// }