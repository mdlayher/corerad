// Package schedgroup provides a goroutine worker pool which schedules tasks
// to be performed at or after a specified time.
//
// Special thanks to Egon Elbre from #performance on Gophers Slack for two
// prototypes (https://play.golang.org/p/YyeSWuDil-b, https://play.golang.org/p/4iYBO6Cgj8m)
// of this idea, based on Go's container/heap package. Egon's prototypes
// heavily influenced the final design of this package.
package schedgroup
