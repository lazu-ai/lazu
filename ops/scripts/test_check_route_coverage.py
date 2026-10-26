import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import check_route_coverage as crc  # noqa: E402


class RoutePatternTest(unittest.TestCase):
    def matches(self, route, text):
        return bool(crc.pattern(route).search(text))

    def test_literal_and_trailing_slash(self):
        self.assertTrue(self.matches('/api/log/', "API.get('/api/log/')"))
        self.assertTrue(self.matches('/api/log/', "API.get('/api/log')"))
        self.assertFalse(self.matches('/api/log/', "API.get('/api/logs')"))
        self.assertFalse(self.matches('/api/log', "API.get('/api/log/stat')"))

    def test_parameters_match_template_placeholder_or_literal(self):
        route = '/api/channel/:id/key'
        self.assertTrue(self.matches(route, 'API.post(`/api/channel/${id}/key`)'))
        self.assertTrue(self.matches(route, '/api/channel/{id}/key:'))
        self.assertTrue(self.matches(route, "'/api/channel/42/key'"))
        self.assertFalse(self.matches(route, "'/api/channel/42/keys'"))

    def test_query_string_counts(self):
        self.assertTrue(self.matches('/api/user/register', "'/api/user/register?turnstile=' + token"))


if __name__ == '__main__':
    unittest.main()
