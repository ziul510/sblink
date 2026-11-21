# Copyright 2026 ziul510
# SPDX-License-Identifier: Apache-2.0

import unittest

from dependency_licenses import CSV, NOTICES, TEMPLATE, Dependency, render, warnings


class DependencyLicenseTest(unittest.TestCase):
    def test_empty_baseline(self):
        files = render(TEMPLATE, {})
        self.assertIn(b"No external Go modules.\n", files[NOTICES])
        self.assertEqual(files[CSV], b"Ecosystem,Dependency,Version,License,Notice SHA256\n")

    def test_version_bump_updates_files_and_warns_only_if_terms_changed(self):
        name = "example.com/lib"
        original = Dependency(name, "v1.0.0", "MIT", ("Old terms\n",))
        version_only = Dependency(name, "v1.1.0", "MIT", original.texts)
        changed_text = Dependency(name, "v1.1.0", "MIT", ("Updated terms\n",))
        changed_license = Dependency(name, "v1.1.0", "Apache-2.0", ("New license\n",))
        before = {name: original}
        self.assertEqual(warnings(before, {name: version_only}), [])
        self.assertIn("text changed", warnings(before, {name: changed_text})[0])
        self.assertIn("license changed", warnings(before, {name: changed_license})[0])
        self.assertIn("dependency added", warnings({}, before)[0])
        self.assertIn("dependency removed", warnings(before, {})[0])
        files = render(TEMPLATE, {name: changed_license})
        self.assertIn(b"example.com/lib v1.1.0 (Apache-2.0)", files[NOTICES])
        self.assertIn(b"Go,example.com/lib,v1.1.0,Apache-2.0,", files[CSV])
        self.assertEqual(files, render(files[NOTICES].decode(), {name: changed_license}))


if __name__ == "__main__":
    unittest.main()
