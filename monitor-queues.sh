#!/bin/bash

# Queue monitoring script for ProSecure Payment API

# Load environment variables for Redis connection
set -a
source .env
set +a

# Extract Redis password from REDIS_URL
REDIS_PASSWORD=$(echo $REDIS_URL | sed -E 's/.*:\/\/(.*):(.*)@.*/\2/')
REDIS_HOST=$(echo $REDIS_URL | sed -E 's/.*@([^:]+).*/\1/')
REDIS_PORT=$(echo $REDIS_URL | sed -E 's/.*:([0-9]+).*/\1/')

# If no password in URL, set empty auth command
if [ -z "$REDIS_PASSWORD" ]; then
	    AUTH_CMD=""
    else
	        AUTH_CMD="AUTH $REDIS_PASSWORD"
fi

echo "=== ProSecure Payment API Queue Monitor ==="
echo "Monitoring Redis at $REDIS_HOST:$REDIS_PORT"
echo "----------------------------------------"

# Function to execute Redis command with authentication
redis_cmd() {
	    if [ -z "$AUTH_CMD" ]; then
		            redis-cli -h $REDIS_HOST -p $REDIS_PORT $@
			        else
					        redis-cli -h $REDIS_HOST -p $REDIS_PORT $AUTH_CMD $@
						    fi
					    }

				    # Get queue statistics
				    MAIN_QUEUE=$(redis_cmd LLEN payment_jobs)
				    PROCESSING_QUEUE=$(redis_cmd LLEN payment_jobs:processing)
				    FAILED_QUEUE=$(redis_cmd LLEN payment_jobs:failed)
				    DELAYED_COUNT=$(redis_cmd ZCARD payment_jobs:delayed)

				    echo "Main queue: $MAIN_QUEUE jobs waiting"
				    echo "Processing: $PROCESSING_QUEUE jobs in progress"
				    echo "Failed queue: $FAILED_QUEUE jobs failed"
				    echo "Delayed queue: $DELAYED_COUNT jobs scheduled for retry"
				    echo "----------------------------------------"

				    # Display failed jobs if any
				    if [ "$FAILED_QUEUE" -gt 0 ]; then
					        echo "Last 5 failed jobs:"
						    for i in {0..4}; do
							            JOB=$(redis_cmd LINDEX payment_jobs:failed $i)
								            if [ ! -z "$JOB" ]; then
										                echo "$JOB" | jq -r '"\(.id) - \(.type) - Retry: \(.retry_count) - Error: \(.data.last_error)"' 2>/dev/null || echo "Could not parse job $i"
												        fi
													    done
													        echo "----------------------------------------"
				    fi

				    # Display delayed jobs if any
				    if [ "$DELAYED_COUNT" -gt 0 ]; then
					        echo "Next 5 scheduled retries:"
						    DELAYED_JOBS=$(redis_cmd ZRANGE payment_jobs:delayed 0 4 WITHSCORES)
						        echo "$DELAYED_JOBS" | awk 'NR%2==1 {job=$0} NR%2==0 {print "Will retry at: " strftime("%Y-%m-%d %H:%M:%S", $0) " - " job}' | sed 's/{"id":"\([^"]*\).*type":"\([^"]*\).*retry_count":\([0-9]*\).*/Job \1 - Type: \2 - Attempt: \3/'
							    echo "----------------------------------------"
				    fi

				    echo "Redis memory usage:"
				    redis_cmd INFO memory | grep used_memory_human
				    echo "----------------------------------------"

				    echo "Use the following commands to manage queues:"
				    echo "  Retry all failed jobs: ./retry-failed.sh"
				    echo "  Clear all queues: ./clear-queues.sh"
				    echo "----------------------------------------"
