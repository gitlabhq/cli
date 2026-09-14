package graph

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2oracle"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

type graphBuilder struct {
	io          *iostreams.IOStreams
	g           *d2graph.Graph
	objsPerNS   map[string]int32 // namespace -> object counter
	knownVertex sets.Set[vertex]
	arc2Key     map[Arc]string
}

func newGraphBuilder(ctx context.Context, io *iostreams.IOStreams) (*graphBuilder, error) {
	_, g, err := d2lib.Compile(ctx, "", nil, nil)
	if err != nil {
		return nil, err
	}
	b := &graphBuilder{
		io:          io,
		g:           g,
		objsPerNS:   map[string]int32{},
		knownVertex: sets.Set[vertex]{},
		arc2Key:     map[Arc]string{},
	}
	err = b.setOnGraph(globals())
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (b *graphBuilder) applyActions(actions []jsonWatchGraphAction) error {
	for _, action := range actions {
		switch {
		case action.SetVertex != nil:
			b.io.LogInfof("Set Vertex %s\n", action.SetVertex.Vertex)
			err := b.handleSetVertex(action.SetVertex)
			if err != nil {
				return err
			}
		case action.DeleteVertex != nil:
			b.io.LogInfof("Delete Vertex %s\n", action.DeleteVertex.Vertex)
			err := b.handleDeleteVertex(action.DeleteVertex)
			if err != nil {
				return err
			}
		case action.SetArc != nil:
			b.io.LogInfof("Set Arc (%s)->(%s)\n", action.SetArc.Source, action.SetArc.Destination)
			err := b.handleSetArc(action.SetArc)
			if err != nil {
				return err
			}
		case action.DeleteArc != nil:
			b.io.LogInfof("Delete Arc (%s)->(%s)\n", action.DeleteArc.Source, action.DeleteArc.Destination)
			err := b.handleDeleteArc(action.DeleteArc)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *graphBuilder) handleSetVertex(sv *jsonSetVertex) error {
	return b.ensureVertex(sv.Vertex)
}

func (b *graphBuilder) ensureVertex(v vertex) error {
	if b.knownVertex.Has(v) {
		return nil
	}
	b.knownVertex.Insert(v)
	switch {
	case v.IsNamespace(): // vertex itself is a Namespace object.
		err := b.ensureNamespace(v.Name)
		if err != nil {
			return err
		}
	case v.IsNamespaced(): // vertex is a namespaced object.
		err := b.ensureNamespace(v.Namespace)
		if err != nil {
			return err
		}
		err = b.createVertex(v)
		if err != nil {
			return err
		}
		b.objsPerNS[v.Namespace]++
	default: // cluster-scoped object, but not a Namespace.
		err := b.createVertex(v)
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *graphBuilder) createVertex(v vertex) error {
	var err error
	vKey := keyForVertex(v)
	b.g, _, err = d2oracle.Create(b.g, nil, vKey)
	if err != nil {
		return err
	}
	err = b.setOnGraph(map[string]any{
		"label":   v.Name,
		"class":   classForVertex(v),
		"tooltip": fmt.Sprintf("%s/%s", v.Group, v.Resource),
	}, vKey)
	if err != nil {
		return err
	}
	return nil
}

func (b *graphBuilder) ensureNamespace(ns string) error {
	if b.isKnownNamespace(ns) {
		return nil
	}
	var err error
	nsV := vertex{
		Group:     "",
		Version:   "v1",
		Resource:  "namespaces",
		Namespace: "",
		Name:      ns,
	}
	nsKey := keyForVertex(nsV)
	b.g, _, err = d2oracle.Create(b.g, nil, nsKey)
	if err != nil {
		return err
	}
	err = b.setOnGraph(map[string]any{
		"label": ns,
		"class": classForVertex(nsV),
	}, nsKey)
	if err != nil {
		return err
	}
	return nil
}

func (b *graphBuilder) handleDeleteVertex(dv *jsonDeleteVertex) error {
	b.knownVertex.Delete(dv.Vertex)
	var err error
	b.g, err = d2oracle.Delete(b.g, nil, keyForVertex(dv.Vertex))
	if err != nil {
		return err
	}
	if !dv.Vertex.IsNamespaced() { // Just deleted a cluster-scoped object, nothing else to do here.
		return nil
	}
	// Need to do some bookkeeping for namespaced objects
	n := b.objsPerNS[dv.Vertex.Namespace] - 1
	if n > 0 { // There are still objects in this namespace
		b.objsPerNS[dv.Vertex.Namespace] = n
		return nil
	}
	// No more objects in this namespace
	delete(b.objsPerNS, dv.Vertex.Namespace)
	if b.isNamespaceKnownAsObject(dv.Vertex.Namespace) {
		// We are watching Namespace objects directly, don't want to remove the vertex since the namespace is still there.
		return nil
	}
	nsV := vertex{
		Group:     "",
		Version:   "v1",
		Resource:  "namespaces",
		Namespace: "",
		Name:      dv.Vertex.Namespace,
	}
	b.g, err = d2oracle.Delete(b.g, nil, keyForVertex(nsV))
	return err
}

func (b *graphBuilder) handleSetArc(sa *jsonSetArc) error {
	if _, ok := b.arc2Key[sa.Arc]; ok { // already exists
		return nil
	}
	err := b.ensureVertex(sa.Source)
	if err != nil {
		return err
	}
	err = b.ensureVertex(sa.Destination)
	if err != nil {
		return err
	}
	aKey := createKeyForArc(sa.Arc)
	var realArcKey string
	b.g, realArcKey, err = d2oracle.Create(b.g, nil, aKey)
	if err != nil {
		return err
	}
	err = b.setOnGraph(map[string]any{
		"class": classForArc(sa.Arc.Type),
	}, realArcKey)
	if err != nil {
		return err
	}
	b.arc2Key[sa.Arc] = realArcKey
	return nil
}

func (b *graphBuilder) handleDeleteArc(da *jsonDeleteArc) error {
	realArcKey := b.arc2Key[da.Arc]
	delete(b.arc2Key, da.Arc)
	var err error
	b.g, err = d2oracle.Delete(b.g, nil, realArcKey)
	// TODO may need to delete vertices!
	return err
}

func (b *graphBuilder) isKnownNamespace(namespace string) bool {
	// We have at least one object in the namespace, or we have the Namespace object.
	return b.objsPerNS[namespace] > 0 || b.isNamespaceKnownAsObject(namespace)
}

func (b *graphBuilder) isNamespaceKnownAsObject(namespace string) bool {
	return b.knownVertex.Has(vertex{
		Group:     "",
		Version:   "v1",
		Resource:  "namespaces",
		Namespace: "",
		Name:      namespace,
	})
}

func (b *graphBuilder) setOnGraph(toSet map[string]any, keys ...string) error {
	for k, v := range toSet {
		var err error
		switch val := v.(type) {
		case string:
			b.g, err = d2oracle.Set(b.g, nil, strings.Join(append(keys, k), "."), nil, new(val))
		case map[string]any:
			err = b.setOnGraph(val, append(keys, k)...)
		default:
			return fmt.Errorf("invalid value type: %T", v)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func classForVertex(vertex vertex) string {
	return classForGR(vertex.Group, vertex.Resource)
}

func classForGR(group, resource string) string {
	return fmt.Sprintf("%s_%s", group, resource)
}

func classForArc(at arcType) string {
	return "arc_" + string(at)
}

// Some group names contain dots, so this function quotes the produced keys to avoid confusing D2:
// otherwise D2 thinks those are nested objects.
func keyForVertex(v vertex) string {
	k := bareKey(v.Group, v.Resource, v.Name)
	if v.Namespace == "" {
		return `"` + k + `"`
	}
	return fmt.Sprintf(`%q.%q`, bareKey("", "namespaces", v.Namespace), k)
}

func bareKey(group, resource, name string) string {
	return fmt.Sprintf("%s/%s/%s", group, resource, name)
}

func createKeyForArc(a Arc) string {
	return fmt.Sprintf("%s -> %s", keyForVertex(a.Source), keyForVertex(a.Destination))
}
