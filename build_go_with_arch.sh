# shellcheck disable=SC2034
export GOOS=$1
export GOARCH=$2

mkdir target

go build -o ./target/$1_$2_binary $3