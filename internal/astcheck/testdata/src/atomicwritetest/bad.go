package atomicwritetest

import "os"

func bad() {
	_ = os.WriteFile("out.txt", []byte("data"), 0o644) // want "os.WriteFile is not atomic; use fsutil.WriteFileAtomic"
}

func alsoBad(data []byte) error {
	return os.WriteFile("other.txt", data, 0o600) // want "os.WriteFile is not atomic; use fsutil.WriteFileAtomic"
}
