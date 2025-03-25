package database

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "time"
    "prosecure-payment-api/models"
)

type Transaction struct {
    tx *sql.Tx
}

func (t *Transaction) Commit() error {
    return t.tx.Commit()
}

func (t *Transaction) Rollback() error {
    return t.tx.Rollback()
}

func (t *Transaction) SavePaymentMethod(masterRef string, card *models.CardData) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    log.Printf("Attempting to save payment method for master ref: %s", masterRef)
    
    query := `
        INSERT INTO billing_infos (master_reference, card, expiry)
        VALUES (?, ?, ?)
    `
    
    maskedCard := "XXXX XXXX XXXX " + card.Number[len(card.Number)-4:]
    
    _, err := t.tx.ExecContext(ctx, query, masterRef, maskedCard, card.Expiry)
    if err != nil {
        log.Printf("Error saving payment method: %v", err)
        return fmt.Errorf("failed to save payment method: %v", err)
    }

    log.Printf("Successfully saved payment method for master ref: %s", masterRef)
    return nil
}

func (t *Transaction) SaveMasterAccount(account *models.MasterAccount) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    log.Printf("Attempting to save master account: %+v", account)
    
    query := `
        INSERT INTO master_accounts (
            name, lname, is_annually, is_trial,
            email, username, plan, purchased_plans,
            simultaneus_users, phone_number, mfa_is_enable,
            renew_date, created_at, total_price,
            reference_uuid, state, city, street,
            zip_code, additional_info
        ) VALUES (
            ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
            1, ?, NOW(), ?, ?, ?, ?, ?, ?, ?
        )
    `
    
    _, err := t.tx.ExecContext(
        ctx,
        query,
        account.Name,
        account.LastName,
        account.IsAnnually,
        account.IsTrial,
        account.Email,
        account.Username,
        account.Plan,
        account.PurchasedPlans,
        account.SimultaneousUsers,
        account.PhoneNumber,
        account.RenewDate,
        account.TotalPrice,
        account.ReferenceUUID,
        account.State,
        account.City,
        account.Street,
        account.ZipCode,
        account.AdditionalInfo,
    )

    if err != nil {
        log.Printf("Error saving master account: %v", err)
        return fmt.Errorf("failed to save master account: %v", err)
    }

    log.Printf("Successfully saved master account with reference: %s", account.ReferenceUUID)
    return nil
}

func (t *Transaction) SaveUser(user *models.User) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    log.Printf("Attempting to save user: %+v", user)

    query := `
        INSERT INTO users (
            master_reference, username, email,
            passphrase, is_master, plan_id,
            toc_accepted, toc_accepted_at, created_at
        ) VALUES (?, ?, ?, ?, ?, ?, 'accepted', NOW(), NOW())
    `
    
    _, err := t.tx.ExecContext(
        ctx,
        query,
        user.MasterReference,
        user.Username,
        user.Email,
        user.Passphrase,
        user.IsMaster,
        user.PlanID,
    )

    if err != nil {
        log.Printf("Error saving user: %v", err)
        return fmt.Errorf("failed to save user: %v", err)
    }

    log.Printf("Successfully saved user with master reference: %s", user.MasterReference)
    return nil
}

func (t *Transaction) SaveInvoice(invoice *models.Invoice) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    log.Printf("Attempting to save invoice: %+v", invoice)

    query := `
        INSERT INTO invoices (
            master_reference, is_trial, total,
            due_date, is_paid, created_at
        ) VALUES (?, ?, ?, ?, ?, NOW())
    `
    
    _, err := t.tx.ExecContext(
        ctx,
        query,
        invoice.MasterReference,
        invoice.IsTrial,
        invoice.Total,
        invoice.DueDate,
        invoice.IsPaid,
    )

    if err != nil {
        log.Printf("Error saving invoice: %v", err)
        return fmt.Errorf("failed to save invoice: %v", err)
    }

    log.Printf("Successfully saved invoice for master reference: %s", invoice.MasterReference)
    return nil
}

func (t *Transaction) SaveTransaction(masterRef string, checkoutID string, amount float64, status string, transactionID string) error {
    log.Printf("Attempting to save transaction: masterRef=%s, checkoutID=%s, amount=%.2f, status=%s, transactionID=%s", 
        masterRef, checkoutID, amount, status, transactionID)

    query := `
        INSERT INTO transactions (
            id, master_reference, checkout_id, amount, status, transaction_id, created_at
        ) VALUES (UUID(), ?, ?, ?, ?, ?, NOW())
    `
    
    _, err := t.tx.Exec(query, masterRef, checkoutID, amount, status, transactionID)
    if err != nil {
        log.Printf("Error saving transaction: %v", err)
        return fmt.Errorf("failed to save transaction: %v", err)
    }

    log.Printf("Successfully saved transaction with ID: %s", transactionID)
    return nil
}

func (t *Transaction) SaveSubscription(masterRef string, status string, nextBillingDate time.Time) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    log.Printf("Attempting to save subscription: masterRef=%s, status=%s, nextBillingDate=%v", 
        masterRef, status, nextBillingDate)

    query := `
        INSERT INTO subscriptions (
            id, master_reference, status, next_billing_date, created_at
        ) VALUES (UUID(), ?, ?, ?, NOW())
    `
    
    _, err := t.tx.ExecContext(ctx, query, masterRef, status, nextBillingDate)
    if err != nil {
        log.Printf("Error saving subscription: %v", err)
        return fmt.Errorf("failed to save subscription: %v", err)
    }

    log.Printf("Successfully saved subscription for master reference: %s", masterRef)
    return nil
}