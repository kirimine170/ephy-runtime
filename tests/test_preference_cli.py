from apps.worker.cli import build_parser


def test_preference_generate_cli_arguments() -> None:
    args = build_parser().parse_args(
        [
            "preference",
            "generate",
            "--dataset",
            "configs/eval.preference.sample.yaml",
            "--role",
            "fast",
            "--count",
            "30",
            "--comparison",
            "prompt_v2_v3",
        ]
    )

    assert args.command == "preference"
    assert args.preference_command == "generate"
    assert args.count == 30
    assert args.comparison == "prompt_v2_v3"
    assert args.temperature > 0


def test_preference_stats_and_export_cli_arguments() -> None:
    stats = build_parser().parse_args(["preference", "stats", "--session", "session-1"])
    exported = build_parser().parse_args(
        [
            "preference",
            "export",
            "--session",
            "session-1",
            "--format",
            "dpo",
            "--output",
            "exports/result.jsonl",
        ]
    )

    assert stats.session == "session-1"
    assert exported.format == "dpo"
    assert exported.output == "exports/result.jsonl"
