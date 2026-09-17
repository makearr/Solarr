package match

import "math"

// Assign answers the minimum-total-cost pairing of rows to columns of a square cost
// matrix (Kuhn-Munkres): assignment[row] is the column that row takes.
func Assign(cost [][]float64) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}
	// One-based potentials and column owners; column 0 is the virtual start column.
	u := make([]float64, n+1)
	v := make([]float64, n+1)
	owner := make([]int, n+1)
	way := make([]int, n+1)
	for row := 1; row <= n; row++ {
		owner[0] = row
		column := 0
		minimum := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := range minimum {
			minimum[j] = math.Inf(1)
		}
		for {
			used[column] = true
			current := owner[column]
			delta, next := math.Inf(1), 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				if reduced := cost[current-1][j-1] - u[current] - v[j]; reduced < minimum[j] {
					minimum[j], way[j] = reduced, column
				}
				if minimum[j] < delta {
					delta, next = minimum[j], j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[owner[j]] += delta
					v[j] -= delta
				} else {
					minimum[j] -= delta
				}
			}
			column = next
			if owner[column] == 0 {
				break
			}
		}
		for column != 0 {
			previous := way[column]
			owner[column] = owner[previous]
			column = previous
		}
	}
	assignment := make([]int, n)
	for j := 1; j <= n; j++ {
		assignment[owner[j]-1] = j - 1
	}
	return assignment
}
