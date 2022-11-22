# shellcheck disable=SC2034
# example ./build_go_with_arch.sh linux amd64 cmd/main.go
export GOOS=$1
export GOARCH=$2

mkdir target

go build -o ./target/$1_$2_binary $3