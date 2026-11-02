package fixture

// Checksum returns the sum of all bytes modulo 256.
func Checksum(data []byte) byte {
	var sum byte
	for i := 0; i < len(data); i++ {
		sum += data[i]
	}
	return sum
}
