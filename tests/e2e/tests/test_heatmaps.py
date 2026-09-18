"""Live Query Builder v5 heatmap requests against seeded metric data."""

import time

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, first_text_block
from fixtures.seeded import create_view, delete_view, extract_view_data, view_gone
from fixtures.telemetry import seed_metrics, wait_for


def _metric_query(metric: str, *, disabled: bool = False, bucket_options: dict | None = None) -> dict:
    spec = {
        "name": "A",
        "signal": "metrics",
        "disabled": disabled,
        "stepInterval": 60,
        "aggregations": [{"metricName": metric, "timeAggregation": "avg", "spaceAggregation": "sum"}],
        "limit": 100,
        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
        "having": {"expression": ""},
    }
    if bucket_options is not None:
        spec["bucketOptions"] = bucket_options
    return {"type": "builder_query", "spec": spec}


def _formula_query(bucket_options: dict, *, disabled: bool = False) -> dict:
    return {
        "type": "builder_formula",
        "spec": {
            "name": "F",
            "expression": "A",
            "disabled": disabled,
            "limit": 100,
            "order": [{"key": {"name": "__result"}, "direction": "desc"}],
            "bucketOptions": bucket_options,
        },
    }


def _query_payload(
    metric: str,
    queries: list[dict],
    *,
    request_type: str = "heatmap",
    start: int | None = None,
    end: int | None = None,
) -> dict:
    if end is None:
        end = int(time.time() * 1000)
    if start is None:
        start = end - 3_600_000
    return {
        "schemaVersion": "v1",
        "start": start,
        "end": end,
        "requestType": request_type,
        "compositeQuery": {"queries": queries},
        "formatOptions": {"formatTableResultForUI": False, "fillGaps": False},
        "variables": {},
    }


def _run_builder_query(mcp_client: MCPClient, query: dict, *, expected_type: str) -> dict:
    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_execute_builder_query",
            {"searchContext": "run the seeded metrics heatmap query", "query": query},
        )
    )
    payload = first_block_json(result)
    data = payload.get("data", payload)
    assert data.get("type") == expected_type, f"wrong {expected_type} response type: {first_text_block(result)[:500]}"
    return data


def _run_heatmap(mcp_client: MCPClient, query: dict) -> dict:
    return _run_builder_query(mcp_client, query, expected_type="heatmap")


def _assert_bucketed(payload: dict, *, minimum_points: int = 1) -> list[float]:
    results = payload.get("data", {}).get("results", [])
    assert results, f"heatmap returned no results: {payload}"
    buckets: list[float] = []
    point_count = 0
    for result in results:
        assert isinstance(result.get("queryName"), str) and result["queryName"]
        aggregations = result.get("aggregations")
        assert isinstance(aggregations, list) and aggregations, f"heatmap result lacks aggregations: {result}"
        for aggregation in aggregations:
            current = aggregation.get("meta", {}).get("buckets")
            assert isinstance(current, list) and current, f"heatmap aggregation lacks meta.buckets: {aggregation}"
            assert current == sorted(current), f"heatmap buckets are not ascending: {current}"
            buckets = current
            series_list = aggregation.get("series")
            assert isinstance(series_list, list) and series_list, f"heatmap aggregation lacks series: {aggregation}"
            for series in series_list:
                if "labels" in series:
                    assert isinstance(series["labels"], list), f"heatmap labels changed shape: {series}"
                values_list = series.get("values")
                assert isinstance(values_list, list) and values_list, f"heatmap series lacks values: {series}"
                for point in values_list:
                    assert "timestamp" in point, f"heatmap point lacks timestamp: {point}"
                    values = point.get("values")
                    assert isinstance(values, list), f"heatmap point lacks bucket values: {point}"
                    assert len(values) == len(current) + 1, (
                        f"expected {len(current) + 1} bucket counts plus overflow, got {len(values)}"
                    )
                    assert "value" not in point, "heatmap point must omit scalar value"
                    point_count += 1
    assert point_count >= minimum_points, f"heatmap returned {point_count} bucketed points"
    return buckets


def _bucket_data_ready(payload: dict) -> dict | None:
    results = payload.get("data", {}).get("results")
    assert isinstance(results, list), f"heatmap results changed shape: {payload}"
    if not results:
        return None
    for result in results:
        assert isinstance(result, dict), f"heatmap result changed shape: {result!r}"
        if result.get("aggregations") is None:
            return None
        assert isinstance(result["aggregations"], list), f"heatmap aggregations changed shape: {result!r}"
        if not result["aggregations"]:
            return None
    return payload


def _time_series_data_ready(payload: dict) -> dict | None:
    results = payload.get("data", {}).get("results")
    assert isinstance(results, list), f"time-series results changed shape: {payload}"
    if not results:
        return None
    for result in results:
        assert isinstance(result, dict), f"time-series result changed shape: {result!r}"
        assert "aggregations" in result, f"time-series result lacks aggregations: {result!r}"
        aggregations = result["aggregations"]
        if aggregations is None:
            continue
        assert isinstance(aggregations, list), f"time-series aggregations changed shape: {result!r}"
        for aggregation in aggregations:
            assert isinstance(aggregation, dict), f"time-series aggregation changed shape: {aggregation!r}"
            assert "series" in aggregation, f"time-series aggregation lacks series: {aggregation!r}"
            series_list = aggregation["series"]
            if series_list is None:
                continue
            assert isinstance(series_list, list), f"time-series series changed shape: {aggregation!r}"
            for series in series_list:
                assert isinstance(series, dict), f"time-series entry changed shape: {series!r}"
                assert "values" in series, f"time-series entry lacks values: {series!r}"
                values = series["values"]
                if values is None:
                    continue
                assert isinstance(values, list), f"time-series values changed shape: {series!r}"
                if values:
                    return payload
    return None


def test_heatmap_absent_log_linear_and_formula_options_round_trip(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_heatmap"
    seed_metrics(f"mcp-e2e-heatmap-{test_id}", metric, value=17.0, count=4, age_seconds=120)

    def metric_visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_list_metrics",
            {"searchContext": f"find heatmap metric {metric}", "searchText": metric, "limit": "100"},
        )
        return not result.get("isError", False) and metric in first_text_block(result)

    wait_for(metric_visible, f"seeded heatmap metric {metric}")

    # Upstream floors the query end to the 60-second step and excludes samples
    # at or after that boundary. Freeze one completed-minute window only after
    # seeding samples safely behind it, then prove those exact bounds through a
    # time-series query before checking heatmap bucketing.
    query_end = int(time.time() // 60) * 60_000
    query_start = query_end - 3_600_000
    time_series_query = _query_payload(
        metric,
        [_metric_query(metric)],
        request_type="time_series",
        start=query_start,
        end=query_end,
    )
    wait_for(
        lambda: _time_series_data_ready(_run_builder_query(mcp_client, time_series_query, expected_type="time_series")),
        f"seeded metric datapoints for {metric} in the fixed query window",
        timeout=60.0,
    )

    def heatmap(queries: list[dict]) -> dict:
        return _query_payload(metric, queries, start=query_start, end=query_end)

    default_query = heatmap([_metric_query(metric)])
    default_payload = wait_for(
        lambda: _bucket_data_ready(_run_heatmap(mcp_client, default_query)),
        f"seeded heatmap datapoints for {metric}",
        timeout=60.0,
    )
    default_buckets = _assert_bucketed(default_payload)
    assert len(default_buckets) > 1

    default_log_payload = _run_heatmap(
        mcp_client, heatmap([_metric_query(metric, bucket_options={"kind": "log", "spec": {"scale": 4}})])
    )
    default_log_buckets = _assert_bucketed(default_log_payload)
    assert default_buckets == pytest.approx(default_log_buckets)

    log_payload = _run_heatmap(
        mcp_client, heatmap([_metric_query(metric, bucket_options={"kind": "log", "spec": {"scale": 1}})])
    )
    log_buckets = _assert_bucketed(log_payload)
    assert log_buckets != pytest.approx(default_buckets), "log scale 1 must differ from the default scale 4"

    linear_payload = _run_heatmap(
        mcp_client,
        heatmap(
            [
                _metric_query(
                    metric,
                    bucket_options={"kind": "linear", "spec": {"maxValue": 100, "numBuckets": 7}},
                )
            ],
        ),
    )
    linear_buckets = _assert_bucketed(linear_payload)
    assert linear_buckets == pytest.approx([100 / 7, 200 / 7])

    formula_payload = _run_heatmap(
        mcp_client,
        heatmap(
            [
                _metric_query(metric, disabled=True),
                _formula_query({"kind": "log", "spec": {"scale": 1}}),
            ],
        ),
    )
    formula_buckets = _assert_bucketed(formula_payload)
    assert formula_buckets == pytest.approx(log_buckets)


def test_saved_view_preserves_heatmap_formula_and_view_shape(mcp_client: MCPClient, test_id: str) -> None:
    name = f"mcp-e2e-heatmap-view-{test_id}"
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_view_heatmap"
    query = _query_payload(
        metric,
        [
            _metric_query(metric, disabled=True),
            _formula_query({"kind": "linear", "spec": {"maxValue": 100, "numBuckets": 9}}),
        ],
    )
    spec = {
        "displayName": name,
        "panelType": "graph",
        "requestType": "heatmap",
        "queries": query["compositeQuery"]["queries"],
    }
    view_id = create_view(mcp_client, name, source="metrics", spec=spec)
    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool("signoz_get_view", {"searchContext": f"get heatmap view {view_id}", "id": view_id})
        )
        data = extract_view_data(fetched)
        assert data["source"] == "metrics"
        assert data["spec"]["panelType"] == "graph"
        assert data["spec"]["requestType"] == "heatmap"
        formula = data["spec"]["queries"][1]["spec"]
        assert formula["bucketOptions"] == {"kind": "linear", "spec": {"maxValue": 100, "numBuckets": 9}}
        assert formula["disabled"] is False
        assert data["spec"]["queries"][0]["spec"]["disabled"] is True

        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_update_view",
                {
                    "searchContext": "update the display name while preserving the heatmap query",
                    "id": view_id,
                    "view": {"source": "metrics", "spec": {**data["spec"], "displayName": name + "-updated"}},
                },
            )
        )
        updated_data = extract_view_data(
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_view",
                    {"searchContext": f"read back updated heatmap view {view_id}", "id": view_id},
                )
            )
        )
        assert updated_data["spec"]["displayName"] == name + "-updated"
        assert updated_data["spec"]["queries"] == data["spec"]["queries"]
        assert updated_data["spec"]["panelType"] == "graph"
    finally:
        delete_view(mcp_client, view_id)
        assert view_gone(mcp_client, view_id), f"heatmap view {view_id} remained after cleanup"
