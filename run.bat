go version
go env -w GOPROXY=https://mirrors.tencentyun.com/go,direct
go env -w GO111MODULE=auto
go mod tidy
::go build -ldflags="-s -w" -o ZeroBot-Plugin.exe
go generate main.go
go run  -ldflags "-s -w" main.go
pause
