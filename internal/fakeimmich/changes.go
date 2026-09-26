package fakeimmich

import "slices"

// SetVisibility changes the visibility of the asset with the given id, as the
// operator does by archiving (archive) or locking (locked) it.
func (s *Server) SetVisibility(id, visibility string) {
	s.change(id, func(a *Asset) { a.Visibility = visibility })
}

// Trash moves the asset with the given id to the trash.
func (s *Server) Trash(id string) {
	s.change(id, func(a *Asset) { a.Trashed = true })
}

// Delete removes the asset with the given id from the library for good.
func (s *Server) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.assetIndex(id)
	s.lib.assets = slices.Delete(s.lib.assets, i, i+1)
}

// change applies f to the asset with the given id.
func (s *Server) change(id string, f func(*Asset)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.lib.assets[s.assetIndex(id)])
}

// assetIndex returns the index of the asset with the given id. s.mu must be
// held.
func (s *Server) assetIndex(id string) int {
	i := slices.IndexFunc(s.lib.assets, func(a Asset) bool { return a.ID == id })
	if i < 0 {
		panic("fakeimmich: no asset " + id)
	}
	return i
}
