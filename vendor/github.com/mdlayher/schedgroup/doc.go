// Package schedgroup provides a goroutine worker pool which schedules tasks
// to be performed at or after a specified time.
//
// Special thanks to Egon Elbre from #performance on Gophers Slack for an
// initial prototype (https://play.golang.org/p/YyeSWuDil-b) of this idea, based
// on Go's container/heap package and a time.Ticker loop. Egon's prototype
// heavily influenced the final design of this package.
package schedgroup
