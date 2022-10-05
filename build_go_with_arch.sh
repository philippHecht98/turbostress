# shellcheck disable=SC2034
GOOS=$1
GOARCH=$2

mkdir out

go build -o ./out $3