package projects

// ConfigForTest exposes the configuration so external tests can build a variant of a service.
func (s *Service) ConfigForTest() Config { return s.cfg }
