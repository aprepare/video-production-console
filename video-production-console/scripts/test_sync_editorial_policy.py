import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("sync_policy", Path(__file__).with_name("sync-editorial-policy.py"))
sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync)


class PolicySyncTests(unittest.TestCase):
    def test_remove_mechanical_gate_bridges_edges_preserves_models_and_history(self):
        policy = dict(self.policy, remove_mechanical_review=True)
        workflow = {"nodes": [
            {"id": "w", "type": "writer", "config": {"model": "chosen", "reasoning_effort": "xhigh", "service_tier": "priority"}},
            {"id": "custom_gate", "type": "selfcheck", "config": {"len_min_ratio": 0.8}},
            {"id": "r", "type": "reviewer", "config": {"model": "review"}},
            {"id": "out", "type": "output", "config": {}}],
            "edges": [["w", "custom_gate"], ["custom_gate", "r"], ["r", "out"]],
            "production": {"voice": "custom"}}
        history = copy.deepcopy(workflow)
        result = sync.update(Path("workflow.json"), workflow, policy)
        self.assertEqual(result["edges"], [["r", "out"], ["w", "r"]])
        self.assertFalse(any(n["type"] == "selfcheck" for n in result["nodes"]))
        self.assertEqual(result["nodes"][0]["config"], history["nodes"][0]["config"])
        self.assertEqual(result["production"], history["production"])
        self.assertEqual(workflow, history)
        self.assertEqual(sync.update(Path("workflow.json"), result, policy), result)

    def setUp(self):
        self.policy = {key: key + " new" for key in (
            "writer_system", "writer_user", "version", "hook", "facts", "facts_offline", "ammo", "reviewer",
            "facts_inject_rule", "ammo_inject_rule", "planner_user_template", "planner_inject_title", "planner_inject_rule")}

    def test_simplify_keeps_models_production_and_is_idempotent(self):
        workflow = {"version": 1, "name": "账号工作流", "production": {"captions_disabled": True, "voice": "custom"},
                    "nodes": [{"id": "source", "type": "input", "config": {}},
                              {"id": "hook", "type": "agent", "title": "钩子分析", "x": 300, "y": 10,
                               "config": {"model": "custom-model", "reasoning_effort": "high", "service_tier": "priority", "system_prompt": "old"}},
                              {"id": "ammo", "type": "agent", "config": {}},
                              {"id": "facts", "type": "agent", "config": {}},
                              {"id": "writer", "type": "writer", "config": {"model": "claude", "prompt_id": "bone_flesh"}},
                              {"id": "review", "type": "reviewer", "config": {"model": "review-model"}}],
                    "edges": [["source", "hook"], ["source", "ammo"], ["source", "facts"], ["ammo", "writer"], ["facts", "writer"], ["hook", "writer"]]}
        original = copy.deepcopy(workflow)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "workflow.json"
            path.write_text(json.dumps(workflow), encoding="utf-8")
            result = sync.update(path, workflow, self.policy)
            self.assertEqual([n["id"] for n in result["nodes"]], ["source", "hook", "writer", "review"])
            hook = result["nodes"][1]
            self.assertEqual(hook["title"], "二创策划")
            self.assertEqual(hook["config"]["user_template"], self.policy["planner_user_template"])
            for key in ("model", "reasoning_effort", "service_tier"):
                self.assertEqual(hook["config"][key], workflow["nodes"][1]["config"][key])
            self.assertEqual(result["production"], workflow["production"])
            self.assertEqual(result["edges"], [["source", "hook"], ["hook", "writer"]])
            path.write_text(json.dumps(result), encoding="utf-8")
            self.assertEqual(sync.update(path, result, self.policy), result)
            self.assertEqual(workflow, original)

    def test_hook_override_is_synced_and_custom_fields_stay(self):
        result = sync.update(Path("agent_prompts.json"), {"hook_system": "old", "custom": "keep"}, self.policy)
        self.assertEqual(result["hook_system"], self.policy["hook"])
        self.assertEqual(result["custom"], "keep")

    def test_reference_migration_preserves_choices_and_updates_all_reference_prompts(self):
        policy = dict(self.policy, reference_system="full draft rules", reference_user_template="original {{source}}", reference_inject_rule="borrow sentences")
        workflow = {"nodes": [
            {"id": "source", "type": "input", "config": {}},
            {"id": "hook", "type": "agent", "title": "二创策划", "config": {"model": "chosen-a", "reasoning_effort": "high", "service_tier": "priority", "system_prompt": "old plan"}},
            {"id": "ref_b", "type": "agent", "title": "参考稿2", "config": {"role": "reference", "model": "chosen-b", "system_prompt": "old draft"}},
            {"id": "writer", "type": "writer", "config": {"model": "writer"}}],
            "edges": [["source", "hook"], ["source", "ref_b"], ["hook", "writer"], ["ref_b", "writer"]]}
        result = sync.update(Path("workflow.json"), workflow, policy)
        self.assertEqual(result["nodes"][1]["config"].get("role"), "reference")
        self.assertEqual(result["nodes"][1]["config"]["model"], "chosen-a")
        self.assertEqual(result["nodes"][1]["config"]["service_tier"], "priority")
        self.assertEqual(result["nodes"][2]["config"]["system_prompt"], "full draft rules")
        self.assertEqual(result["edges"], workflow["edges"])
        self.assertEqual(sync.update(Path("workflow.json"), result, policy), result)


if __name__ == "__main__":
    unittest.main()
