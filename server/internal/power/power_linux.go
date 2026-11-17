package power

// systemd-inhibit holds a logind sleep/idle lock while "cat" runs, i.e. until
// it is stopped or the server's end of the pipe closes. Without systemd (or
// when the lock isn't allowed) the Keeper logs a warning and carries on.
var inhibitCommand = []string{
	"systemd-inhibit", "--what=sleep:idle", "--who=MiniMedia",
	"--why=Streaming to the TV", "--mode=block", "cat",
}
