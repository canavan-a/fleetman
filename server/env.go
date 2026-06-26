package main

import "os"

// lookupEnv wraps os.LookupEnv for testability.
var lookupEnv = os.LookupEnv
