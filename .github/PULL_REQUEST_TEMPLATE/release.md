# Release vFIXME

## Checklist

- [ ] [Create a new github milestone](https://github.com/DataDog/dd-trace-go/milestones/new) for the next version and
  move any open PRs and issues that are in the current release milestone to the new one.

- [ ] Draft [the release note](https://github.com/DataDog/dd-trace-go/releases/new) and ask the teams for its review on
  the `#guild-dd-go` channel.

- Deploy a release candidate:
    - Create a release candidate version on this release branch:
        - [ ] Bump and commit the release candidate version string in `/internal/version/version.go` (eg. `v1.2.3-rc.4`)
        - [ ] Tag the bump commit with the release candidate version (eg. `git tag v1.2.3-rc.4`)
        - [ ] Push the tag and commit (eg. `git push --tags origin release-v1.2.3`)
    - Deploy some Datadog services to staging:
        - [ ] Give a heads-up on the channel `#staging-headsup` that you are starting the deployment.
        - [ ] Create a draft pull request on dd-go upgrading dd-trace-go to the release candidate tag:
          ```console
          dd-go/$ git checkout prod
          dd-go/$ git pull
          dd-go/$ git checkout -b dd-trace-go-v1.2.3
          dd-go/$ go get -u -v gopkg.in/DataDog/dd-trace-go.v1@v1.2.3-rc.4
          dd-go/$ go mod tidy
          dd-go/$ git add go.mod go.sum
          dd-go/$ git commit -m '[go.mod] upgrade dd-trace-go to v1.2.3-rc.4'
          dd-go/$ git push -u origin dd-trace-go-v1.2.3
          ```
        - [ ] Deploy some services to staging:
          ```console
          dd-go/$ env GITLAB_TOKEN=MY_TOKEN to-staging trace-sampler \
                                                 trace-fetcher \
                                                 trace-firehose-writer \
                                                 trace-stats-aggregator \
                                                 trace-stats-extractor \
                                                 trace-stats-query \
                                                 fleet-config-delivery \
                                                 runtime-security-rule-checker \
                                                 appsec \
                                                 fleet-edge \
                                                 fleet-api \
                                                 service-catalog \
                                                 trace-edge
          ```
        - [ ] Check the deployment is successful on the [Go tracer dashboard]
    - [ ] Deploy the reliability-environment services:
        - [ ] Run [the gitlab pipeline](https://gitlab.ddbuild.io/DataDog/datadog-reliability-env/-/pipelines/new)
          with the variable `DD_TRACE_GO_CANDIDATE_VERSION` set to the release candidate version you want to deploy.
        - [ ] Check the deployment is successful on the [Go rel-env dashboard].
    - [ ] Update every dd-trace-go team on `#guild-dd-go` so that they can perform any further checks.

- Let the release candidate run on staging and rel-env for at least 24 hours and review all the dashboards, especially:
    - [ ] Check the memory usage trend of rel-env which should be flat over time on the [Go rel-env dashboard].
    - [ ] Check de deployment tab of some deployed staging services to see if there was any negative impact on the
      service latency and memory usage (
      eg. https://ddstaging.datadoghq.com/apm/services/trace-edge/operations/http.request/deployments).
    - [ ] Check through the [Go tracer dashboard] to look for any anomalies such as high memory or cpu use, increased
      number of dropped traces, increase in tracer errors.

- If anything went wrong during this release candidate, you can fix the problems on this release branch and go through a
  new release candidate version as many times as required.

- [ ] If required by this release, update the public documentation.

- If everything went well, you are now good to finish the release:
    - [ ] Remove the release candidate version suffix from the version string in `/internal/version/version.go`.
    - [ ] Commit and push the change.
    - [ ] Finish this release pull request by automatically merging and tagging the release by setting the
      label `bot/release/merge` to this pull request.
    - [ ] Publish the release draft.
    - [ ] Update the dd-go draft pull request to the release tag and merge it into dd-go's `prod` branch.
    - [ ] Heads-up on the slack channels `#go` and `#guild-dd-go` about the release and the dd-go's `prod` update.
    - [ ] Close the milestone of the released version.

[Go tracer dashboard]: https://ddstaging.datadoghq.com/dashboard/r92-2p7-shv/go-tracer

[Go rel-env dashboard]: https://ddstaging.datadoghq.com/dashboard/s2a-5wy-g5b/go-reliability-env-dashboard
