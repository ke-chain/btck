package db

import (
	"fmt"
	"math/big"
	"strconv"
)

func String2Int(s string) (i int) {
	x, err := strconv.Atoi(s)
	if err != nil {
		fmt.Println("String2Int err:", err)
	}
	return x
}

func StringToBigInt(s string) *big.Int {
	n := new(big.Int)
	n, ok := n.SetString(s, 10)
	if !ok {
		fmt.Println("SetString: error")
	}
	return n
}

func String2Int64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fmt.Printf("s% %d of type %T", String2Int64, n, n)
	}
	return n
}

func Int2Int64String(i int) (s string) {

	s = strconv.FormatInt(int64(i), 10)

	return s
}
