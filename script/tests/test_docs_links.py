import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location(
    'check_docs', Path(__file__).resolve().parents[1] / 'check_docs.py')
check_docs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check_docs)


class DocumentationHeadingTests(unittest.TestCase):
    def test_multilingual_navigation_targets(self):
        anchors = check_docs.markdown_anchors(
            '## Automatic outage evidence\n## 故障自動取證\n### 更新 S2L 应用\n')
        self.assertIn('automatic-outage-evidence', anchors)
        self.assertIn('故障自動取證', anchors)
        self.assertIn('更新-s2l-应用', anchors)
        self.assertNotIn('missing-heading', anchors)

    def test_example_comments_cannot_supply_heading_targets(self):
        anchors = check_docs.markdown_anchors(
            '```bash\n# Upgrade\n```\n~~~python\n# Hidden\n~~~\n## Visible\n')
        self.assertEqual(anchors, {'visible'})

    def test_repeated_headings_have_distinct_targets(self):
        anchors = check_docs.markdown_anchors('## DNS / TCP\n## DNS / TCP\n')
        self.assertEqual(anchors, {'dns--tcp', 'dns--tcp-1'})


if __name__ == '__main__':
    unittest.main()
