// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

type fixtureFile struct{ Name, Media, Content string }

type examTemplate struct {
	Title, Instructions, Clarification string
	Directories                        []string
	Files, Resources                   []fixtureFile
}

func examinationTemplate(index int) examTemplate {
	if index%3 == 1 {
		return examTemplate{
			Title:        "Software Testing — Draft",
			Instructions: "Design a regression-testing exercise for a small library. The teaching team is still preparing the starter material.\n",
		}
	}
	// Skip the bare drafts when rotating populated scenarios, ensuring that
	// every template has published examples in the development profiles.
	switch (index - (index+1)/3) % 3 {
	case 0:
		return programmingTemplate()
	case 1:
		return databaseTemplate()
	default:
		return analysisTemplate()
	}
}

func programmingTemplate() examTemplate {
	return examTemplate{
		Title:         "Programming Fundamentals — Stable Deduplication",
		Instructions:  "## Instructions\n\nImplement `unique_records` in `src/lib/records.py`. Return distinct values in their original order without modifying the input. Run the example with `python src/main.py`.\n\n1. Inspect the sample records.\n2. Explain time and space complexity in `README.md`.\n3. Add tests for empty input, repeated values, and Unicode text.\n\n> All names and records in this exercise are fictional.\n\n## Examples\n\n| Input | Expected output |\n|---|---|\n| `[]` | `[]` |\n| `[3, 1, 3]` | `[3, 1]` |\n| `['é', 'e', 'é']` | `['é', 'e']` |\n",
		Clarification: "Treat text as case-sensitive. Include a test demonstrating that `A` and `a` remain distinct.\n",
		Directories:   []string{"src", "src/lib", "tests", "notes", "empty-directory"},
		Files: []fixtureFile{
			{"README.md", "text/markdown", "# Stable deduplication\n\nExplain your approach, complexity, and test results here.\n"},
			{"src/main.py", "text/plain", "from lib.records import unique_records\n\nif __name__ == '__main__':\n    print(unique_records([3, 1, 3]))\n"},
			{"src/lib/records.py", "text/plain", "def unique_records(records):\n    \"\"\"Return distinct values in their original order.\"\"\"\n    raise NotImplementedError('Complete this function')\n"},
			{"tests/test_records.py", "text/plain", "from src.lib.records import unique_records\n\ndef test_empty_input():\n    assert unique_records([]) == []\n"},
			{"notes/résumé.md", "text/markdown", "# Notes\n\nRecord observations about Unicode and edge cases.\n"},
			{"notes/design-decisions-and-test-observations-for-the-final-implementation.md", "text/markdown", ""},
		},
		Resources: []fixtureFile{
			{"Reference notes.md", "text/markdown", "# Reference notes\n\nPreserve input order. Equality is exact; do not normalize text. Consider empty input and repeated values.\n"},
			{"Sample observations.csv", "text/csv", "record_id,label,value\n1,North,12\n2,South,7\n3,North,12\n4,East,7\n"},
		},
	}
}

func databaseTemplate() examTemplate {
	return examTemplate{
		Title:         "Database Design — Library Loans",
		Instructions:  "## Library reporting\n\nUse the SQLite schema in `schema.sql` and the synthetic records in `fixtures/loans.sql`. Complete `queries/overdue.sql` to list outstanding loans due before **2030-03-15**, ordered by due date and loan ID.\n\nA returned loan must be excluded. The reference date is fixed so the result does not depend on your machine's clock. Explain the required indexes in `answer.md`.\n",
		Clarification: "A loan due on 2030-03-15 is not overdue. Dates use ISO 8601 calendar notation.\n",
		Directories:   []string{"queries", "fixtures"},
		Files: []fixtureFile{
			{"schema.sql", "text/plain", "CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT NOT NULL);\nCREATE TABLE loans (id INTEGER PRIMARY KEY, book_id INTEGER NOT NULL REFERENCES books(id), due_on TEXT NOT NULL, returned_on TEXT);\n"},
			{"fixtures/loans.sql", "text/plain", "INSERT INTO books VALUES (1, 'A Guide to Algorithms'), (2, 'Data in Practice');\nINSERT INTO loans VALUES (1, 1, '2030-03-10', NULL), (2, 2, '2030-03-12', '2030-03-13'), (3, 2, '2030-03-15', NULL);\n"},
			{"queries/overdue.sql", "text/plain", "-- Return loan_id, title, and due_on for overdue, outstanding loans.\n-- Write your query here.\n"},
			{"answer.md", "text/markdown", "# Query design\n\n## Indexes\n\n## Boundary cases\n"},
		},
		Resources: []fixtureFile{
			{"Library schema.md", "text/markdown", "# Relationships\n\nEach loan refers to one book. A book can have several historical loans. `returned_on` is NULL while a loan is outstanding.\n"},
			{"Expected overdue loans.csv", "text/csv", "loan_id,title,due_on\n1,A Guide to Algorithms,2030-03-10\n"},
			{"Report parameters.json", "application/json", "{\"reference_date\":\"2030-03-15\",\"sort\":[\"due_on\",\"loan_id\"]}\n"},
		},
	}
}

func analysisTemplate() examTemplate {
	return examTemplate{
		Title:         "Data Analysis — Sensor Quality",
		Instructions:  "## Scenario\n\nA campus laboratory receives hourly readings from three fictional sensors. Its next report must separate missing observations from valid zero measurements. Work only with the supplied local files.\n\n## Tasks\n\n1. Read `data/raw/readings.csv` with Python's standard `csv` module.\n2. Complete `analysis/summary.py` to group rows by sensor and compute the mean of non-empty values.\n3. Include an observation count and a missing-value count for every sensor.\n4. When a sensor has no valid observations, return `None` for its mean.\n5. Preserve zero readings and reject non-numeric values with a useful error.\n\n## Output contract\n\nReturn a dictionary keyed by sensor ID. Every value must contain `count`, `missing`, and `mean`. Keep full precision while computing; presentation rounding belongs in your report.\n\n```json\n{\"north\": {\"count\": 2, \"missing\": 1, \"mean\": 10.0}}\n```\n\n## Checks and discussion\n\nRun `python -m unittest discover -s tests`. Add boundary cases beyond the supplied zero-value test, including an empty file and an entirely missing series. Record assumptions in `reports/quality.md`, with one section explaining how missing data changes interpretation.\n\n> Sensor identifiers and readings are synthetic. No network services or additional packages are required.\n",
		Clarification: "Whitespace-only values count as missing. A numeric zero remains a valid observation and contributes to the mean.\n",
		Directories:   []string{"analysis", "data", "data/raw", "reports", "reports/figures", "tests"},
		Files: []fixtureFile{
			{"analysis/summary.py", "text/plain", "def summarize(rows):\n    \"\"\"Return count, missing count, and mean by sensor.\"\"\"\n    raise NotImplementedError('Complete the summary')\n"},
			{"data/raw/readings.csv", "text/csv", "sensor,value\nnorth,8\nnorth,\nnorth,12\nsouth,0\nsouth,4\nwest,\n"},
			{"tests/test_summary.py", "text/plain", "import unittest\nfrom analysis.summary import summarize\n\nclass SummaryTests(unittest.TestCase):\n    def test_zero_is_valid(self):\n        result = summarize([{'sensor': 'south', 'value': '0'}])\n        self.assertEqual(result['south'], {'count': 1, 'missing': 0, 'mean': 0.0})\n"},
			{"reports/quality.md", "text/markdown", "# Sensor quality report\n\n## Results\n\n## Missing observations\n\n## Limitations\n"},
		},
		Resources: []fixtureFile{
			{"Data dictionary.json", "application/json", "{\"sensor\":\"fictional sensor identifier\",\"value\":\"numeric reading or empty field\",\"unit\":\"degrees Celsius\"}\n"},
		},
	}
}
