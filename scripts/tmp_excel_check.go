package main
import (
  "fmt"
  "os"
  "time"
)
func main() {
  loc, _ := time.LoadLocation("Europe/Rome")
  for _, v := range []string{"46012.3592361111", "46012.4564583333"} {
    f, _ := time.ParseDuration("0s")
    _ = f
    var x float64
    fmt.Sscanf(v, "%f", &x)
    t := time.Date(1899, 12, 30, 0, 0, 0, 0, loc).Add(time.Duration(x * float64(24*time.Hour)))
    fmt.Printf("%s -> %s\n", v, t.Format(time.RFC3339))
  }
  _ = os.Stdout
}
