module BrailleBar/print-job-service

go 1.27.1

// Lets Docker build this service on its own (GOWORK=off); locally go.work is used.
replace BrailleBar/platform => ../platform
