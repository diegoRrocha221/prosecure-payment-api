package authorizenet

type createTransactionRequestWrapper struct {
    CreateTransactionRequest createTransactionRequest `json:"createTransactionRequest"`
}

type createTransactionRequest struct {
    MerchantAuthentication merchantAuthenticationType `json:"merchantAuthentication"`
    RefID                 string                    `json:"refId,omitempty"`
    TransactionRequest    transactionRequestType    `json:"transactionRequest"`
}

type merchantAuthenticationType struct {
    Name           string `json:"name"`
    TransactionKey string `json:"transactionKey"`
}

type CreditCardType struct {
    CardNumber     string `json:"cardNumber"`
    ExpirationDate string `json:"expirationDate"`
    CardCode       string `json:"cardCode"`
}

type PaymentType struct {
    CreditCard CreditCardType `json:"creditCard"`
}

type transactionRequestType struct {
    TransactionType    string       `json:"transactionType"`
    Amount            string       `json:"amount,omitempty"`
    Payment           *PaymentType `json:"payment,omitempty"`
    RefTransId        string       `json:"refTransId,omitempty"`
}

type MessageType struct {
    Code        string `json:"code"`
    Text        string `json:"text"`
    Description string `json:"description,omitempty"`
}

type MessagesType struct {
    ResultCode string        `json:"resultCode"`
    Message    []MessageType `json:"message"`
}

type transactionResponse struct {
    ResponseCode   string        `json:"responseCode"`
    AuthCode      string        `json:"authCode"`
    AVSResultCode string        `json:"avsResultCode"`
    CVVResultCode string        `json:"cvvResultCode"`
    TransID       string        `json:"transId"`
    RefTransID    string        `json:"refTransId"`
    Messages      []MessageType `json:"messages,omitempty"`
}

type createTransactionResponse struct {
    TransactionResponse transactionResponse `json:"transactionResponse"`
    Messages          MessagesType        `json:"messages"`
}

// ARB Types
type ARBSubscriptionRequest struct {
    MerchantAuthentication merchantAuthenticationType `json:"merchantAuthentication"`
    RefID                 string                    `json:"refId"`
    Subscription         ARBSubscriptionType       `json:"subscription"`
}

type ARBSubscriptionType struct {
    Name            string             `json:"name"`
    PaymentSchedule PaymentScheduleType `json:"paymentSchedule"`
    Amount         string             `json:"amount"`
    Payment        PaymentType        `json:"payment"`
    Order          OrderType          `json:"order"`
    Customer       CustomerType       `json:"customer"`
    BillTo         CustomerAddressType `json:"billTo"`
}

type PaymentScheduleType struct {
    Interval         IntervalType `json:"interval"`
    StartDate       string       `json:"startDate"`
    TotalOccurrences string       `json:"totalOccurrences"`
}

type IntervalType struct {
    Length int    `json:"length"`
    Unit   string `json:"unit"`
}

type CustomerType struct {
    Type        string `json:"type"`
    Email       string `json:"email"`
    PhoneNumber string `json:"phoneNumber,omitempty"`
}

type OrderType struct {
    InvoiceNumber string `json:"invoiceNumber"`
    Description   string `json:"description"`
}

type CustomerAddressType struct {
    FirstName string `json:"firstName"`
    LastName  string `json:"lastName"`
    Address   string `json:"address"`
    City      string `json:"city"`
    State     string `json:"state"`
    Zip       string `json:"zip"`
    Country   string `json:"country"`
}

// ARB Response Types
type ARBResponse struct {
    RefID         string       `json:"refId"`
    SubscriptionID string       `json:"subscriptionId,omitempty"`
    Messages      MessagesType `json:"messages"`
}