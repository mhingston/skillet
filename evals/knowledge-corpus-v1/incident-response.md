# Incident Response

This runbook covers production incidents and service recovery.

## SEV-1 rollback procedure

When a production outage follows a deployment, declare the incident, preserve evidence, and rollback to the last verified release. Confirm recovery before closing the SEV-1.

## Recovery objective

Track recovery time separately from customer-impact duration. A rollback is a mitigation, not proof that the underlying fault is fixed.
