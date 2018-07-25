package main

import (
	"html/template"
	"os"
)

func main() {
	interfaces := []string{"Flusher", "Pusher", "CloseNotifier", "Hijacker"}
	var combos [][][]string
	for pick := len(interfaces); pick > 0; pick-- {
		combos = append(combos, combinations(interfaces, pick))
	}

	template.Must(template.ParseFiles("./gen.gohtml")).Execute(os.Stdout, map[string]interface{}{
		"Interfaces":   interfaces,
		"Combinations": combos,
	})
}

func combinations(strs []string, pick int) (all [][]string) {
	switch pick {
	case 0:
	case 1:
		for i := range strs {
			all = append(all, strs[i:i+1])
		}
	default:
		for i := range strs {
			for _, next := range combinations(strs[i+1:], pick-1) {
				all = append(all, append([]string{strs[i]}, next...))
			}
		}
	}
	return all
}
