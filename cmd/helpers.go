package main

// Background() helper function accepts an arbitrary function as parameter
func (app *application) background(fn func()) {
	app.wg.Add(1)
	// launch background goroutine
	go func() {
		defer app.wg.Done()
		// recover panic
		defer func() {
			if err := recover(); err != nil {
				app.logger.Error("goroutine panic", "error", err)
			}
		}()

		// Exec arbitrary func
		fn()
	}()
}
