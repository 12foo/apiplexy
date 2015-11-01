go get -t ./...
CGO_CFLAGS=`pkg-config luajit --cflags`
CGO_LDFLAGS=`pkg-config luajit --libs`
go get -f -u github.com/aarzilli/golua/lua
