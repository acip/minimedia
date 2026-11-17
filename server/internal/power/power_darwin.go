package power

// caffeinate -i prevents idle sleep while it runs; it reads nothing and is
// stopped by killing it ("cat" keeps it running until stdin closes).
var inhibitCommand = []string{"caffeinate", "-i", "cat"}
