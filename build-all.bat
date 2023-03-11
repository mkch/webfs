SET GOOS=darwin
SET GOARCH=amd64
go build -o bin/webfs-darwin-amd64

SET GOOS=windows
SET GOARCH=amd64
go build -o bin/webfs-windows-amd64.exe

SET GOOS=linux
SET GOARCH=amd64
go build -o bin/webfs-linux-amd64

SET GOOS=linux
SET GOARCH=arm
go build -o bin/webfs-linux-arm
