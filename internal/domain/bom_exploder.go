package domain

// BomExploder — pure logic, tidak boleh ada DB/Redis call di sini.
// Implementasi asli (recursive explosion, net requirement calc) belum
// ditulis di stub ini — lihat test case wajib di spec section 6.4
// sebelum Task 3.2 dianggap selesai:
//
//   1. Single-level BOM, qty bulat
//   2. Multi-level BOM (3+ level nested)
//   3. Qty pecahan (rounding error tidak boleh terakumulasi)
//   4. Circular BOM reference — harus return error, bukan infinite loop
//   5. BOM versioning — pakai versi aktif sesuai effective_date
//   6. Nested manufactured item (komponen yang punya BOM sendiri)
//   7. BOM kosong / produk tanpa BOM terdaftar — return error jelas
//   8. Net requirement dikurangi stock on hand, bukan gross requirement
//
// Struct BomComponent di bawah cuma placeholder minimal untuk stub
// worker bisa compile dan dites end-to-end sebelum logic asli ditulis.

type BomComponent struct {
	VariantID       int64
	QuantityPerUnit float64
	ScrapPct        float64
	IsPhantom       bool
}

// Explode adalah stub — return slice kosong. Implementasi asli
// (recursive CTE hasil query dibawa ke Go, atau full recursion di Go)
// perlu didesain terpisah dan ditest lewat 8 kasus di atas sebelum
// dipakai worker.go untuk kalkulasi sungguhan.
func Explode(bomVersionID int64, quantity float64) ([]BomComponent, error) {
	return []BomComponent{}, nil
}
