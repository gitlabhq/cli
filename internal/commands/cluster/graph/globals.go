package graph

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type arcType string

const (
	ownerReferenceArcType      arcType = "or"
	referenceArcType           arcType = "r"
	transitiveReferenceArcType arcType = "t"
)

var globals = sync.OnceValue(constructGlobals)

func constructGlobals() map[string]any {
	// Shapes: https://d2lang.com/tour/shapes/
	// Styling properties: https://d2lang.com/tour/style
	classesGR := map[schema.GroupResource]map[string]any{
		{Group: "", Resource: "namespaces"}: {
			"style.stroke-width": "2",
			"style.stroke-dash":  "3",
			"style.fill":         "#FAFAFA",
			"style.fill-pattern": "grain",
		},
		{Group: "", Resource: "configmaps"}: {
			"shape":      "page",
			"style.fill": "#77DEDE",
		},
		{Group: "", Resource: "pods"}: {
			"shape":      "parallelogram",
			"style.fill": "#E5F3FF",
		},
		{Group: "", Resource: "secrets"}: {
			"style.fill": "#FCC31E",
		},
		{Group: "apps", Resource: "replicasets"}: {
			"style.fill": "#BCDDFB",
		},
		{Group: "apps", Resource: "deployments"}: {
			"style.fill": "#87BFF3",
		},
	}
	classesArc := map[arcType]map[string]any{
		ownerReferenceArcType: {
			"style.stroke-dash": "3",
			"style.stroke":      "gold",
		},
		referenceArcType: {},
		transitiveReferenceArcType: {
			"style.stroke-dash": "3",
			"style.stroke":      "deepskyblue",
		},
	}
	gl := map[string]any{
		// "cluster-scoped": map[string]any{
		//	"direction": "down",
		// },
		// TODO vars to set layout, legend, etc, etc
	}

	for gr, v := range classesGR {
		gl["classes."+classForGR(gr.Group, gr.Resource)] = v
	}
	for at, style := range classesArc {
		gl["classes."+classForArc(at)] = style
	}
	return gl
}
